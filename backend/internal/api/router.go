package api

import (
	"context"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"spinwheel/internal/auth"
	"spinwheel/internal/config"
	"spinwheel/internal/httpx"
	"spinwheel/internal/store"
)

// Server porte les dépendances des gestionnaires HTTP.
type Server struct {
	cfg  *config.Config
	st   *store.Store
	log  *slog.Logger
	auth *auth.Auth
}

// Politique de sécurité du contenu commune à toutes les pages.
//
// `default-src 'none'` ferme tout par défaut ; chaque type de ressource est
// rouvert explicitement. Pas de 'unsafe-inline' : ni script ni style en ligne
// dans le HTML, ce qui neutralise la quasi-totalité des vecteurs XSS réflexifs.
const cspBase = "default-src 'none'; " +
	"script-src 'self'; " +
	"style-src 'self'; " +
	"img-src 'self' data:; " +
	"font-src 'self'; " +
	"connect-src 'self'; " +
	"form-action 'self'; " +
	"base-uri 'none'; " +
	"object-src 'none'"

// NewRouter construit le routeur complet du service.
func NewRouter(ctx context.Context, cfg *config.Config, st *store.Store, a *auth.Auth, log *slog.Logger) http.Handler {
	s := &Server{cfg: cfg, st: st, log: log, auth: a}

	appCSP := cspBase + "; frame-ancestors 'none'"
	embedCSP := cspBase + "; frame-ancestors " + cfg.EmbedFrameAncestors

	appSec := httpx.SecurityHeaders(httpx.SecurityOptions{
		CSP:                       appCSP,
		FrameDeny:                 true,
		HSTS:                      cfg.CookieSecure,
		CrossOriginOpenerPolicy:   "same-origin",
		CrossOriginResourcePolicy: "same-origin",
	})
	// Page d'intégration : pas de X-Frame-Options (cet en-tête ne sait pas
	// exprimer une liste d'origines), la restriction passe par
	// frame-ancestors. Pas de COOP non plus : elle isolerait la page de son
	// parent sans rien apporter ici.
	embedSec := httpx.SecurityHeaders(httpx.SecurityOptions{
		CSP:  embedCSP,
		HSTS: cfg.CookieSecure,
	})

	origin := cfg.BaseURL.String()
	sameOrigin := httpx.SameOriginWrite(origin)

	// --- Limitation de débit ---
	//
	// Les seuils sont calibrés pour une classe entière derrière une seule IP
	// publique : un quota trop serré bloquerait les élèves 11 à 25 d'une même
	// salle avant de gêner qui que ce soit de mal intentionné.
	authLimiter := httpx.NewLimiter(20, 10)    // connexions
	apiLimiter := httpx.NewLimiter(240, 60)    // API authentifiée
	spinLimiter := httpx.NewLimiter(120, 40)   // tirages, par IP et par roue
	uploadLimiter := httpx.NewLimiter(10, 20)  // images, par compte
	embedLimiter := httpx.NewLimiter(120, 40)  // lecture publique d'une roue
	assetLimiter := httpx.NewLimiter(600, 200) // fichiers images des segments
	for _, l := range []*httpx.Limiter{
		authLimiter, apiLimiter, spinLimiter, uploadLimiter, embedLimiter, assetLimiter,
	} {
		l.StartJanitor(ctx)
	}

	byIP := func(r *http.Request) string { return httpx.ClientIP(r, cfg.TrustProxy) }
	byUser := func(r *http.Request) string {
		if u, ok := auth.UserOf(r.Context()); ok {
			return u.ID.String()
		}
		return httpx.ClientIP(r, cfg.TrustProxy)
	}
	// Le refus est journalisé sur la sortie standard, pas en base : écrire une
	// ligne d'audit par requête refusée transformerait une inondation en
	// inondation d'écritures.
	onBlock := func(r *http.Request, key string) {
		log.Warn("débit dépassé",
			"request_id", httpx.RequestIDOf(r),
			"path", r.URL.Path,
			"ip", httpx.ClientIP(r, cfg.TrustProxy))
	}

	// --- Chaînes d'intergiciels ---
	jsonBase := func(extra ...httpx.Middleware) []httpx.Middleware {
		base := []httpx.Middleware{
			appSec,
			httpx.NoStore(),
			httpx.MaxBody(cfg.MaxJSONBodyBytes),
			sameOrigin,
		}
		return append(base, extra...)
	}

	// Route d'API authentifiée : session obligatoire + jeton CSRF.
	authed := func(h http.HandlerFunc) http.Handler {
		return httpx.Chain(h, jsonBase(
			httpx.RateLimit(apiLimiter, byIP, onBlock),
			a.Resolve,
			a.RequireUser,
			a.CSRF,
		)...)
	}

	// Route d'API publique (iframe) : ni session ni CSRF — il n'y a aucune
	// autorité ambiante à détourner, seul le débit est bridé.
	public := func(h http.HandlerFunc, limiter *httpx.Limiter, key httpx.RateLimitFunc) http.Handler {
		return httpx.Chain(h, jsonBase(httpx.RateLimit(limiter, key, onBlock))...)
	}

	mux := http.NewServeMux()

	// --- Santé ---
	mux.Handle("GET /healthz", httpx.Chain(http.HandlerFunc(s.health), appSec, httpx.NoStore()))

	// --- Connexion ---
	mux.Handle("GET /auth/google/start", httpx.Chain(
		http.HandlerFunc(a.Start), appSec, httpx.NoStore(),
		httpx.RateLimit(authLimiter, byIP, onBlock)))
	mux.Handle("GET /auth/google/callback", httpx.Chain(
		http.HandlerFunc(a.Callback), appSec, httpx.NoStore(),
		httpx.RateLimit(authLimiter, byIP, onBlock)))

	// --- Session ---
	mux.Handle("GET /api/me", httpx.Chain(http.HandlerFunc(a.Me),
		jsonBase(httpx.RateLimit(apiLimiter, byIP, onBlock), a.Resolve)...))
	mux.Handle("POST /api/logout", authed(a.Logout))

	// --- Roues ---
	mux.Handle("GET /api/wheels", authed(s.listWheels))
	mux.Handle("POST /api/wheels", authed(s.createWheel))
	mux.Handle("GET /api/wheels/{id}", authed(s.getWheel))
	mux.Handle("PATCH /api/wheels/{id}", authed(s.patchWheel))
	mux.Handle("DELETE /api/wheels/{id}", authed(s.deleteWheel))
	mux.Handle("PUT /api/wheels/{id}/segments", authed(s.putSegments))
	mux.Handle("GET /api/wheels/{id}/spins", authed(s.listSpins))

	// --- Images ---
	mux.Handle("GET /api/images", authed(s.listImages))
	mux.Handle("POST /api/images", httpx.Chain(http.HandlerFunc(s.uploadImage),
		appSec,
		httpx.NoStore(),
		// Marge au-dessus de la taille du fichier pour l'enveloppe multipart.
		httpx.MaxBody(cfg.MaxImageBytes+(1<<20)),
		sameOrigin,
		a.Resolve,
		a.RequireUser,
		a.CSRF,
		httpx.RateLimit(uploadLimiter, byUser, onBlock),
	))

	// --- Liste blanche ---
	mux.Handle("GET /api/allowed-emails", authed(s.listAllowed))
	mux.Handle("POST /api/allowed-emails", authed(s.addAllowed))
	mux.Handle("DELETE /api/allowed-emails/{id}", authed(s.deleteAllowed))

	// --- API publique de l'iframe ---
	mux.Handle("GET /api/embed/{id}", public(s.getEmbedWheel, embedLimiter, byIP))
	// Clé sur la seule adresse IP, jamais sur l'identifiant de roue : celui-ci
	// vient de l'URL, donc de l'appelant. Le faire entrer dans la clé offrirait
	// un seau neuf à chaque requête — quota contourné et table de seaux sans
	// borne. La protection par roue reste assurée en base, par le plafond
	// horaire de maxSpinsPerWheelPerHour.
	mux.Handle("POST /api/embed/{id}/spin", public(s.spin, spinLimiter, byIP))

	// --- Fichiers images ---
	// Servis en clair par identifiant : ils doivent être lisibles depuis
	// l'iframe, qui n'a pas de session.
	mux.Handle("GET /img/{id}", httpx.Chain(http.HandlerFunc(s.serveImage),
		httpx.RateLimit(assetLimiter, byIP, onBlock)))

	// --- Pages ---
	mux.Handle("GET /{$}", httpx.Chain(s.page("index.html"), appSec))
	mux.Handle("GET /wheels/{id}", httpx.Chain(s.page("edit.html"), appSec))
	mux.Handle("GET /comptes", httpx.Chain(s.page("accounts.html"), appSec))
	mux.Handle("GET /embed/{id}", httpx.Chain(s.page("embed.html"), embedSec))

	// --- Ressources statiques ---
	staticDir := filepath.Join(cfg.FrontendDir, "static")
	// noDirList enveloppe StripPrefix, pas l'inverse : il doit voir le chemin
	// d'origine. Une fois le préfixe retiré, « /static/ » devient la chaîne
	// vide et ne ressemble plus à un dossier.
	files := noDirList(http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))
	mux.Handle("GET /static/", httpx.Chain(files, appSec, cacheFor(5*time.Minute)))

	return httpx.Chain(mux,
		httpx.RequestID(),
		httpx.Recover(log),
		httpx.AccessLog(log, cfg.TrustProxy),
	)
}

// page sert un fichier HTML du dossier front. Le nom vient toujours du code,
// jamais de la requête.
func (s *Server) page(name string) http.Handler {
	path := filepath.Join(s.cfg.FrontendDir, name)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, path)
	})
}

// health répond 200 si la base est joignable.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.st.Ping(ctx); err != nil {
		s.log.Error("sonde de santé : base injoignable", "error", err)
		httpx.JSON(w, http.StatusServiceUnavailable, map[string]any{"status": "degraded"})
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// noDirList empêche http.FileServer de lister le contenu d'un dossier.
//
// Un chemin vide compte comme un dossier : c'est ce que devient « /static/ »
// après StripPrefix, et c'est exactement le cas qui produisait un index
// cliquable de tout l'arbre statique.
func noDirList(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "" || strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// cacheFor pose une durée de cache sur les ressources statiques.
func cacheFor(d time.Duration) httpx.Middleware {
	value := "public, max-age=" + strconv.Itoa(int(d.Seconds()))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", value)
			next.ServeHTTP(w, r)
		})
	}
}

// audit écrit une ligne de journal d'audit enrichie du contexte HTTP.
func (s *Server) audit(r *http.Request, e store.AuditEntry) {
	e.IP = httpx.ClientIP(r, s.cfg.TrustProxy)
	e.UserAgent = r.UserAgent()
	e.RequestID = httpx.RequestIDOf(r)
	if err := s.st.Audit(r.Context(), e); err != nil {
		s.log.Error("écriture du journal d'audit",
			"error", err, "action", e.Action, "request_id", e.RequestID)
	}
}
