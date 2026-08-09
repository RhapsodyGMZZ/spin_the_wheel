// Package store regroupe l'accès à PostgreSQL : pool de connexions,
// migrations et requêtes métier.
//
// Toutes les requêtes passent par des paramètres liés ($1, $2, ...) : aucune
// valeur venant de l'utilisateur n'est concaténée dans du SQL.
package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"spinwheel/internal/uid"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// ErrNotFound est renvoyée quand une ligne attendue n'existe pas.
var ErrNotFound = errors.New("store: introuvable")

// Store porte le pool de connexions.
type Store struct {
	Pool *pgxpool.Pool
}

// Open ouvre le pool et vérifie que la base répond.
func Open(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("DATABASE_URL illisible: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MinConns = 1
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 15 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("ouverture du pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("la base ne répond pas: %w", err)
	}
	return &Store{Pool: pool}, nil
}

// Close ferme le pool.
func (s *Store) Close() { s.Pool.Close() }

// Ping vérifie que la base répond.
func (s *Store) Ping(ctx context.Context) error { return s.Pool.Ping(ctx) }

var migrationNameRe = regexp.MustCompile(`^[0-9]{4}_[a-z0-9_]+\.sql$`)

// Migrate applique les fichiers de migration non encore joués, dans l'ordre
// lexicographique de leur nom.
//
// Chaque fichier est envoyé en une seule requête « simple » : PostgreSQL
// l'exécute alors dans une transaction implicite, et l'enregistrement de la
// version est concaténé au même envoi. Une migration est donc appliquée en
// entier ou pas du tout.
func (s *Store) Migrate(ctx context.Context, log *slog.Logger) error {
	conn, err := s.Pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquisition d'une connexion: %w", err)
	}
	defer conn.Release()

	pg := conn.Conn().PgConn()

	const createTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	);`
	if _, err := pg.Exec(ctx, createTable).ReadAll(); err != nil {
		return fmt.Errorf("création de schema_migrations: %w", err)
	}

	applied := map[string]bool{}
	rows, err := conn.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("lecture de schema_migrations: %w", err)
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !migrationNameRe.MatchString(e.Name()) {
			return fmt.Errorf("nom de migration non conforme: %q", e.Name())
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		if applied[name] {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		// Le nom vient d'un embed validé par migrationNameRe : pas de quote à
		// échapper. Le littéral est tout de même construit explicitement.
		batch := string(body) + "\n;\nINSERT INTO schema_migrations (version) VALUES ('" + name + "');"
		if _, err := pg.Exec(ctx, batch).ReadAll(); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
		log.Info("migration appliquée", "version", name)
	}
	return nil
}

// BootstrapAllowedEmails insère les adresses fournies par l'environnement dans
// la liste blanche, sans écraser celles déjà présentes.
func (s *Store) BootstrapAllowedEmails(ctx context.Context, emails []string, log *slog.Logger) error {
	for _, e := range emails {
		email := strings.ToLower(strings.TrimSpace(e))
		if email == "" {
			continue
		}
		tag, err := s.Pool.Exec(ctx, `
			INSERT INTO allowed_emails (id, email, note)
			VALUES ($1, $2, 'ajouté au démarrage via BOOTSTRAP_ADMIN_EMAILS')
			ON CONFLICT (email) DO NOTHING`,
			uid.New(), email)
		if err != nil {
			return fmt.Errorf("bootstrap de %s: %w", email, err)
		}
		if tag.RowsAffected() > 0 {
			log.Info("adresse ajoutée à la liste blanche", "email", email)
		}
	}
	return nil
}

// PurgeExpired supprime les sessions et états OAuth périmés. Appelé
// périodiquement par une tâche de fond.
func (s *Store) PurgeExpired(ctx context.Context) (sessions, states int64, err error) {
	tag, err := s.Pool.Exec(ctx,
		`DELETE FROM sessions WHERE expires_at < now() - interval '7 days'`)
	if err != nil {
		return 0, 0, err
	}
	sessions = tag.RowsAffected()

	tag, err = s.Pool.Exec(ctx,
		`DELETE FROM oauth_states WHERE expires_at < now() - interval '1 hour'`)
	if err != nil {
		return sessions, 0, err
	}
	return sessions, tag.RowsAffected(), nil
}

// nullUUID rend nil pour l'UUID nul, afin d'insérer NULL plutôt que
// '00000000-...' dans une colonne optionnelle.
func nullUUID(u uid.UUID) any {
	if u.IsZero() {
		return nil
	}
	return u
}

// isNoRows indique si l'erreur pgx correspond à un résultat vide.
func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
