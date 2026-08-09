package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"spinwheel/internal/logging"
	"spinwheel/internal/uid"
)

type ctxKey int

const ctxKeyRequestID ctxKey = iota

// Middleware est un intergiciel HTTP.
type Middleware func(http.Handler) http.Handler

// Chain applique les intergiciels de gauche à droite : Chain(h, a, b) exécute
// a puis b puis h.
func Chain(h http.Handler, mw ...Middleware) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}

// RequestID attache un identifiant unique à chaque requête et le renvoie dans
// l'en-tête X-Request-Id. C'est la clé qui relie une ligne de log, une ligne
// d'audit et une erreur affichée au client.
//
// L'identifiant est toujours généré côté serveur : un en-tête entrant est
// ignoré, sinon un client pourrait polluer ou brouiller les journaux.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := uid.New().String()
			ctx := context.WithValue(r.Context(), ctxKeyRequestID, id)
			ctx = logging.WithRequestID(ctx, id)
			w.Header().Set("X-Request-Id", id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// Recover transforme une panique en 500 et journalise la pile.
func Recover(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					// http.ErrAbortHandler est le signal normal d'un client parti.
					if rec == http.ErrAbortHandler {
						panic(rec)
					}
					log.Error("panique",
						"request_id", RequestIDOf(r),
						"method", r.Method,
						"path", r.URL.Path,
						"panic", rec,
						"stack", string(debug.Stack()),
					)
					Err(w, r, http.StatusInternalServerError, CodeInternal, "Erreur interne.")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// statusWriter capte le code de statut et le volume écrit.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
		w.ResponseWriter.WriteHeader(code)
	}
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// AccessLog journalise chaque requête servie.
func AccessLog(log *slog.Logger, trustProxy bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w}
			next.ServeHTTP(sw, r)
			if sw.status == 0 {
				sw.status = http.StatusOK
			}

			lvl := slog.LevelInfo
			switch {
			case sw.status >= 500:
				lvl = slog.LevelError
			case sw.status >= 400:
				lvl = slog.LevelWarn
			}

			log.Log(r.Context(), lvl, "requête",
				"request_id", RequestIDOf(r),
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.status,
				"bytes", sw.bytes,
				"duration_ms", time.Since(start).Milliseconds(),
				"ip", ClientIP(r, trustProxy),
				"user_agent", truncate(r.UserAgent(), 256),
			)
		})
	}
}

// SecurityOptions décrit les en-têtes de sécurité d'un groupe de routes.
type SecurityOptions struct {
	// CSP est la valeur complète de Content-Security-Policy.
	CSP string
	// FrameDeny pose X-Frame-Options: DENY. À laisser à false sur les routes
	// destinées à être encadrées : cet en-tête ne sait pas exprimer une liste
	// d'origines, seul frame-ancestors le peut.
	FrameDeny bool
	// HSTS pose Strict-Transport-Security (uniquement derrière TLS).
	HSTS bool
	// CrossOriginOpenerPolicy vaut "" pour ne pas poser l'en-tête.
	CrossOriginOpenerPolicy string
	// CrossOriginResourcePolicy vaut "" pour ne pas poser l'en-tête.
	CrossOriginResourcePolicy string
}

// SecurityHeaders pose les en-têtes de sécurité communs.
func SecurityHeaders(o SecurityOptions) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			if o.CSP != "" {
				h.Set("Content-Security-Policy", o.CSP)
			}
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Permissions-Policy",
				"accelerometer=(), autoplay=(), camera=(), display-capture=(), "+
					"encrypted-media=(), fullscreen=(self), geolocation=(), gyroscope=(), "+
					"magnetometer=(), microphone=(), midi=(), payment=(), usb=(), xr-spatial-tracking=()")
			if o.FrameDeny {
				h.Set("X-Frame-Options", "DENY")
			}
			if o.HSTS {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}
			if o.CrossOriginOpenerPolicy != "" {
				h.Set("Cross-Origin-Opener-Policy", o.CrossOriginOpenerPolicy)
			}
			if o.CrossOriginResourcePolicy != "" {
				h.Set("Cross-Origin-Resource-Policy", o.CrossOriginResourcePolicy)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// MaxBody borne la taille du corps de requête. Au-delà, la lecture échoue
// plutôt que de charger des mégaoctets en mémoire.
func MaxBody(n int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}

// NoStore interdit toute mise en cache de la réponse.
func NoStore() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store, max-age=0")
			next.ServeHTTP(w, r)
		})
	}
}

// SameOriginWrite exige que les requêtes modifiantes proviennent de l'origine
// du site. Deuxième barrière CSRF, indépendante du jeton : elle tient même si
// le jeton fuit, et elle ne coûte rien.
//
// Le contrôle s'appuie sur Sec-Fetch-Site quand le navigateur l'envoie, sinon
// sur Origin. Une requête sans aucun des deux est refusée sur les méthodes
// modifiantes.
func SameOriginWrite(expectedOrigin string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isSafeMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			switch r.Header.Get("Sec-Fetch-Site") {
			case "same-origin", "none":
				next.ServeHTTP(w, r)
				return
			case "cross-site", "same-site":
				Err(w, r, http.StatusForbidden, CodeForbidden, "Origine non autorisée.")
				return
			}
			// Navigateur sans Sec-Fetch-Site : on retombe sur Origin.
			if origin := r.Header.Get("Origin"); origin != "" {
				if strings.EqualFold(origin, expectedOrigin) {
					next.ServeHTTP(w, r)
					return
				}
				Err(w, r, http.StatusForbidden, CodeForbidden, "Origine non autorisée.")
				return
			}
			Err(w, r, http.StatusForbidden, CodeForbidden, "Origine absente.")
		})
	}
}

func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
