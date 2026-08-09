// Commande server : point d'entrée du service Spin the Wheel.
//
// Le serveur écoute en HTTP en clair, sur la boucle locale du conteneur. Le
// chiffrement, les certificats et la terminaison TLS sont l'affaire du reverse
// proxy nginx placé devant.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"spinwheel/internal/api"
	"spinwheel/internal/auth"
	"spinwheel/internal/config"
	"spinwheel/internal/logging"
	"spinwheel/internal/store"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false,
		"interroge /healthz sur l'instance locale et sort avec 0 si elle répond")
	flag.Parse()

	if *healthcheck {
		os.Exit(runHealthcheck())
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "démarrage impossible : %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := logging.New(cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Le dossier d'images est un volume monté : il peut exister ou non.
	if err := os.MkdirAll(cfg.ImageDir, 0o750); err != nil {
		return fmt.Errorf("dossier des images %s: %w", cfg.ImageDir, err)
	}

	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.Migrate(ctx, log); err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	if err := st.BootstrapAllowedEmails(ctx, cfg.BootstrapEmails, log); err != nil {
		return fmt.Errorf("liste blanche initiale: %w", err)
	}

	a := auth.New(cfg, st, log)
	handler := api.NewRouter(ctx, cfg, st, a, log)

	srv := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: handler,
		// En-têtes lus vite, corps lu sans traîner : deux garde-fous contre
		// les connexions volontairement lentes (Slowloris).
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          nil,
	}

	go backgroundJanitor(ctx, st, cfg, log)

	errCh := make(chan error, 1)
	go func() {
		log.Info("serveur démarré",
			"addr", cfg.HTTPAddr,
			"base_url", cfg.BaseURL.String(),
			"env", cfg.Env,
			"frame_ancestors", cfg.EmbedFrameAncestors,
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("arrêt demandé, fermeture en cours")
	}

	// Laisser les requêtes en cours se terminer avant de couper.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("arrêt brutal", "error", err)
		return err
	}
	log.Info("serveur arrêté proprement")
	return nil
}

// backgroundJanitor fait le ménage périodique : sessions périmées, états OAuth
// abandonnés, images jamais rattachées à un segment.
func backgroundJanitor(ctx context.Context, st *store.Store, cfg *config.Config, log *slog.Logger) {
	const period = time.Hour
	t := time.NewTicker(period)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			jobCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)

			sessions, states, err := st.PurgeExpired(jobCtx)
			if err != nil {
				log.Error("purge des sessions", "error", err)
			} else if sessions+states > 0 {
				log.Info("purge effectuée", "sessions", sessions, "etats_oauth", states)
			}

			// 24 h de grâce : une image téléversée puis laissée de côté le
			// temps de finir l'édition ne disparaît pas.
			if n := api.PurgeOrphanImages(jobCtx, st, cfg.ImageDir, 24*time.Hour, log); n > 0 {
				log.Info("images orphelines supprimées", "count", n)
			}

			cancel()
		}
	}
}

// runHealthcheck interroge /healthz sur l'instance locale. Utilisé par la
// sonde de santé Docker, l'image finale n'ayant ni shell ni curl.
func runHealthcheck() int {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}

	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get("http://" + addr + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		return 1
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck: HTTP", resp.StatusCode)
		return 1
	}
	return 0
}
