package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"spinwheel/internal/uid"
)

// User est un compte ayant ouvert au moins une session.
type User struct {
	ID          uid.UUID  `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
}

// Session est une session serveur active.
type Session struct {
	ID        uid.UUID
	UserID    uid.UUID
	CSRFToken string
	ExpiresAt time.Time
}

// AllowedEmail est une entrée de la liste blanche.
type AllowedEmail struct {
	ID        uid.UUID  `json:"id"`
	Email     string    `json:"email"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
}

// --- Liste blanche ----------------------------------------------------------

// IsEmailAllowed indique si l'adresse figure dans la liste blanche.
func (s *Store) IsEmailAllowed(ctx context.Context, email string) (bool, error) {
	var exists bool
	err := s.Pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM allowed_emails WHERE email = $1)`,
		strings.ToLower(email)).Scan(&exists)
	return exists, err
}

// ListAllowedEmails rend la liste blanche, la plus récente d'abord.
func (s *Store) ListAllowedEmails(ctx context.Context) ([]AllowedEmail, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT id, email, note, created_at FROM allowed_emails ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []AllowedEmail{}
	for rows.Next() {
		var a AllowedEmail
		if err := rows.Scan(&a.ID, &a.Email, &a.Note, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// AddAllowedEmail ajoute une adresse à la liste blanche.
func (s *Store) AddAllowedEmail(ctx context.Context, email, note string, addedBy uid.UUID) (AllowedEmail, error) {
	a := AllowedEmail{ID: uid.New(), Email: strings.ToLower(email), Note: note}
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO allowed_emails (id, email, note, added_by)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (email) DO UPDATE SET note = EXCLUDED.note
		RETURNING id, email, note, created_at`,
		a.ID, a.Email, note, nullUUID(addedBy),
	).Scan(&a.ID, &a.Email, &a.Note, &a.CreatedAt)
	return a, err
}

// DeleteAllowedEmail retire une adresse de la liste blanche et révoque les
// sessions ouvertes du compte correspondant.
func (s *Store) DeleteAllowedEmail(ctx context.Context, id uid.UUID) (string, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op après Commit

	var email string
	if err := tx.QueryRow(ctx,
		`DELETE FROM allowed_emails WHERE id = $1 RETURNING email`, id).Scan(&email); err != nil {
		if isNoRows(err) {
			return "", ErrNotFound
		}
		return "", err
	}

	// Retirer l'accès doit fermer immédiatement les sessions en cours.
	if _, err := tx.Exec(ctx, `
		UPDATE sessions SET revoked_at = now()
		WHERE revoked_at IS NULL
		  AND user_id IN (SELECT id FROM users WHERE email = $1)`, email); err != nil {
		return "", err
	}
	return email, tx.Commit(ctx)
}

// --- Comptes ----------------------------------------------------------------

// UpsertUser crée ou met à jour le compte associé à un identifiant Google.
func (s *Store) UpsertUser(ctx context.Context, googleSub, email, displayName string) (User, error) {
	var u User
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO users (id, google_sub, email, display_name, last_login_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (google_sub) DO UPDATE SET
			email         = EXCLUDED.email,
			display_name  = EXCLUDED.display_name,
			last_login_at = now(),
			updated_at    = now()
		RETURNING id, email, display_name, created_at`,
		uid.New(), googleSub, strings.ToLower(email), displayName,
	).Scan(&u.ID, &u.Email, &u.DisplayName, &u.CreatedAt)
	if err != nil {
		return User{}, fmt.Errorf("upsert du compte: %w", err)
	}
	return u, nil
}

// --- État OAuth -------------------------------------------------------------

// CreateOAuthState mémorise un état anti-CSRF et son vérificateur PKCE.
func (s *Store) CreateOAuthState(ctx context.Context, stateHash []byte, verifier string, ttl time.Duration) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO oauth_states (id, state_hash, code_verifier, expires_at)
		VALUES ($1, $2, $3, $4)`,
		uid.New(), stateHash, verifier, time.Now().Add(ttl).UTC())
	return err
}

// ConsumeOAuthState rend le vérificateur PKCE associé à un état, et marque
// l'état comme consommé. Un second appel avec le même état échoue : c'est ce
// qui empêche le rejeu d'un code d'autorisation intercepté.
func (s *Store) ConsumeOAuthState(ctx context.Context, stateHash []byte) (string, error) {
	var verifier string
	err := s.Pool.QueryRow(ctx, `
		UPDATE oauth_states
		SET consumed_at = now()
		WHERE state_hash = $1
		  AND consumed_at IS NULL
		  AND expires_at > now()
		RETURNING code_verifier`, stateHash).Scan(&verifier)
	if err != nil {
		if isNoRows(err) {
			return "", ErrNotFound
		}
		return "", err
	}
	return verifier, nil
}

// --- Sessions ---------------------------------------------------------------

// CreateSession ouvre une session pour un compte.
func (s *Store) CreateSession(ctx context.Context, userID uid.UUID, tokenHash []byte, csrf, ip, userAgent string, ttl time.Duration) (Session, error) {
	sess := Session{
		ID:        uid.New(),
		UserID:    userID,
		CSRFToken: csrf,
		ExpiresAt: time.Now().Add(ttl).UTC(),
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO sessions (id, user_id, token_hash, csrf_token, ip, user_agent, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		sess.ID, userID, tokenHash, csrf, ip, truncate(userAgent, 512), sess.ExpiresAt)
	if err != nil {
		return Session{}, fmt.Errorf("création de session: %w", err)
	}
	return sess, nil
}

// LookupSession résout un jeton de session en session + compte.
// Renvoie ErrNotFound si la session est inconnue, révoquée ou périmée.
func (s *Store) LookupSession(ctx context.Context, tokenHash []byte) (Session, User, error) {
	var sess Session
	var u User
	err := s.Pool.QueryRow(ctx, `
		SELECT s.id, s.user_id, s.csrf_token, s.expires_at,
		       u.id, u.email, u.display_name, u.created_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1
		  AND s.revoked_at IS NULL
		  AND s.expires_at > now()`, tokenHash,
	).Scan(&sess.ID, &sess.UserID, &sess.CSRFToken, &sess.ExpiresAt,
		&u.ID, &u.Email, &u.DisplayName, &u.CreatedAt)
	if err != nil {
		if isNoRows(err) {
			return Session{}, User{}, ErrNotFound
		}
		return Session{}, User{}, err
	}
	return sess, u, nil
}

// TouchSession met à jour la date de dernière activité, au plus une fois par
// minute pour éviter une écriture à chaque requête.
func (s *Store) TouchSession(ctx context.Context, id uid.UUID) error {
	_, err := s.Pool.Exec(ctx,
		`UPDATE sessions SET last_seen_at = now()
		 WHERE id = $1 AND last_seen_at < now() - interval '1 minute'`, id)
	return err
}

// RevokeSession ferme une session (déconnexion).
func (s *Store) RevokeSession(ctx context.Context, tokenHash []byte) error {
	_, err := s.Pool.Exec(ctx,
		`UPDATE sessions SET revoked_at = now()
		 WHERE token_hash = $1 AND revoked_at IS NULL`, tokenHash)
	return err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
