package auth

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"spinwheel/internal/config"
	"spinwheel/internal/httpx"
	"spinwheel/internal/store"
)

// Durée de validité d'une tentative de connexion entamée.
const oauthStateTTL = 10 * time.Minute

// Auth expose les routes de connexion et les intergiciels d'authentification.
type Auth struct {
	cfg    *config.Config
	st     *store.Store
	log    *slog.Logger
	google *GoogleClient
}

// New construit le service d'authentification.
func New(cfg *config.Config, st *store.Store, log *slog.Logger) *Auth {
	return &Auth{
		cfg:    cfg,
		st:     st,
		log:    log,
		google: NewGoogleClient(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.OAuthRedirectURL),
	}
}

// audit écrit une ligne de journal d'audit enrichie du contexte de la requête.
func (a *Auth) audit(r *http.Request, e store.AuditEntry) {
	e.IP = httpx.ClientIP(r, a.cfg.TrustProxy)
	e.UserAgent = r.UserAgent()
	e.RequestID = httpx.RequestIDOf(r)
	if err := a.st.Audit(r.Context(), e); err != nil {
		a.log.Error("écriture du journal d'audit",
			"error", err, "action", e.Action, "request_id", e.RequestID)
	}
}

// failLogin renvoie l'utilisateur sur l'accueil avec un code d'erreur pris
// dans un ensemble fermé, jamais un message venant de Google.
func (a *Auth) failLogin(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/?erreur="+code, http.StatusSeeOther)
}

// Start démarre la connexion Google. GET /auth/google/start
func (a *Auth) Start(w http.ResponseWriter, r *http.Request) {
	verifier, err := newVerifier()
	if err != nil {
		a.log.Error("génération du vérificateur PKCE", "error", err)
		a.failLogin(w, r, "interne")
		return
	}
	state, err := newToken()
	if err != nil {
		a.log.Error("génération de l'état OAuth", "error", err)
		a.failLogin(w, r, "interne")
		return
	}

	if err := a.st.CreateOAuthState(r.Context(), hashToken(state), verifier, oauthStateTTL); err != nil {
		a.log.Error("enregistrement de l'état OAuth", "error", err)
		a.failLogin(w, r, "interne")
		return
	}

	// L'état vit à la fois en base (usage unique) et dans un cookie : le
	// cookie lie la tentative à CE navigateur, ce qui bloque la connexion
	// forcée (login CSRF) où un tiers ferait aboutir sa propre session chez
	// la victime.
	a.setCookie(w, a.oauthCookieName(), state, oauthStateTTL)

	a.audit(r, store.AuditEntry{Action: store.ActionLoginStarted})

	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, a.google.AuthURL(state, challengeOf(verifier)), http.StatusSeeOther)
}

// Callback termine la connexion Google. GET /auth/google/callback
func (a *Auth) Callback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	w.Header().Set("Cache-Control", "no-store")

	cookieName := a.oauthCookieName()
	stateCookie := ""
	if c, err := r.Cookie(cookieName); err == nil {
		stateCookie = c.Value
	}
	// Effacé maintenant, pas en fin de fonction : le premier http.Redirect
	// écrit les en-têtes, et un Set-Cookie posé après serait perdu.
	a.clearCookie(w, cookieName)

	q := r.URL.Query()

	if q.Get("error") != "" {
		// L'utilisateur a refusé, ou Google a rejeté la demande.
		a.audit(r, store.AuditEntry{
			Action:  store.ActionLoginFailed,
			Details: map[string]any{"raison": "refus_google"},
		})
		a.failLogin(w, r, "refus_google")
		return
	}

	code := q.Get("code")
	state := q.Get("state")
	if code == "" || state == "" {
		a.failLogin(w, r, "requete_invalide")
		return
	}

	if stateCookie == "" ||
		subtle.ConstantTimeCompare([]byte(stateCookie), []byte(state)) != 1 {
		a.audit(r, store.AuditEntry{
			Action:  store.ActionLoginFailed,
			Details: map[string]any{"raison": "etat_non_concordant"},
		})
		a.failLogin(w, r, "etat_invalide")
		return
	}

	verifier, err := a.st.ConsumeOAuthState(ctx, hashToken(state))
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			a.log.Error("consommation de l'état OAuth", "error", err)
		}
		a.audit(r, store.AuditEntry{
			Action:  store.ActionLoginFailed,
			Details: map[string]any{"raison": "etat_inconnu_ou_perime"},
		})
		a.failLogin(w, r, "etat_invalide")
		return
	}

	accessToken, err := a.google.Exchange(ctx, code, verifier)
	if err != nil {
		a.log.Error("échange du code OAuth", "error", err, "request_id", httpx.RequestIDOf(r))
		a.audit(r, store.AuditEntry{
			Action:  store.ActionLoginFailed,
			Details: map[string]any{"raison": "echange_refuse"},
		})
		a.failLogin(w, r, "google")
		return
	}

	profile, err := a.google.UserInfo(ctx, accessToken)
	if err != nil {
		a.log.Error("lecture du profil Google", "error", err, "request_id", httpx.RequestIDOf(r))
		a.failLogin(w, r, "google")
		return
	}

	email := strings.ToLower(strings.TrimSpace(profile.Email))

	if !profile.EmailVerified {
		a.audit(r, store.AuditEntry{
			Action:  store.ActionLoginDenied,
			Details: map[string]any{"email": email, "raison": "email_non_verifie"},
		})
		a.failLogin(w, r, "email_non_verifie")
		return
	}

	allowed, err := a.st.IsEmailAllowed(ctx, email)
	if err != nil {
		a.log.Error("consultation de la liste blanche", "error", err)
		a.failLogin(w, r, "interne")
		return
	}
	if !allowed {
		a.audit(r, store.AuditEntry{
			Action:  store.ActionLoginDenied,
			Details: map[string]any{"email": email, "raison": "hors_liste_blanche"},
		})
		a.failLogin(w, r, "non_autorise")
		return
	}

	user, err := a.st.UpsertUser(ctx, profile.Sub, email, profile.Name)
	if err != nil {
		a.log.Error("enregistrement du compte", "error", err)
		a.failLogin(w, r, "interne")
		return
	}

	token, err := newToken()
	if err != nil {
		a.log.Error("génération du jeton de session", "error", err)
		a.failLogin(w, r, "interne")
		return
	}
	csrf, err := newToken()
	if err != nil {
		a.log.Error("génération du jeton CSRF", "error", err)
		a.failLogin(w, r, "interne")
		return
	}

	if _, err := a.st.CreateSession(ctx, user.ID, hashToken(token), csrf,
		httpx.ClientIP(r, a.cfg.TrustProxy), r.UserAgent(), a.cfg.SessionTTL); err != nil {
		a.log.Error("ouverture de session", "error", err)
		a.failLogin(w, r, "interne")
		return
	}

	a.setCookie(w, a.cfg.CookieName, token, a.cfg.SessionTTL)
	a.audit(r, store.AuditEntry{
		ActorID:    user.ID,
		Action:     store.ActionLoginSucceeded,
		EntityType: "user",
		EntityID:   user.ID,
		Details:    map[string]any{"email": user.Email},
	})

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// Logout ferme la session courante. POST /api/logout
func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(a.cfg.CookieName); err == nil && c.Value != "" {
		if err := a.st.RevokeSession(r.Context(), hashToken(c.Value)); err != nil {
			a.log.Error("révocation de session", "error", err)
		}
	}
	a.clearCookie(w, a.cfg.CookieName)

	var actor = store.User{}
	if u, ok := UserOf(r.Context()); ok {
		actor = u
	}
	a.audit(r, store.AuditEntry{ActorID: actor.ID, Action: store.ActionLogout})

	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Me décrit la session courante. GET /api/me
//
// C'est aussi le point où le front récupère son jeton CSRF : le jeton n'est
// jamais posé dans un cookie lisible par JavaScript, il transite uniquement
// dans le corps de cette réponse puis dans l'en-tête X-CSRF-Token.
func (a *Auth) Me(w http.ResponseWriter, r *http.Request) {
	u, ok := UserOf(r.Context())
	if !ok {
		httpx.JSON(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}
	sess, _ := SessionOf(r.Context())
	httpx.JSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"user": map[string]any{
			"id":           u.ID,
			"email":        u.Email,
			"display_name": u.DisplayName,
		},
		"csrf_token": sess.CSRFToken,
	})
}

// --- Intergiciels -----------------------------------------------------------

// Resolve attache le compte au contexte s'il y a une session valide, sans
// jamais refuser la requête. Utile pour les pages qui s'affichent
// différemment selon l'état de connexion.
func (a *Auth) Resolve(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(a.cfg.CookieName)
		if err != nil || c.Value == "" {
			next.ServeHTTP(w, r)
			return
		}
		sess, user, err := a.st.LookupSession(r.Context(), hashToken(c.Value))
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				// Panne de base, pool saturé, connexion coupée : la session
				// est peut-être parfaitement valide. Effacer le cookie ici
				// déconnecterait tout le monde pour quelques secondes
				// d'indisponibilité, en laissant autant de sessions orphelines
				// en base. On signale l'indisponibilité, on ne touche à rien.
				a.log.Error("lecture de session", "error", err)
				httpx.Err(w, r, http.StatusServiceUnavailable, httpx.CodeInternal,
					"Service momentanément indisponible.")
				return
			}
			// Session réellement inconnue, révoquée ou périmée : le cookie ne
			// sert plus à rien, autant éviter que le navigateur le renvoie.
			a.clearCookie(w, a.cfg.CookieName)
			next.ServeHTTP(w, r)
			return
		}
		if err := a.st.TouchSession(r.Context(), sess.ID); err != nil {
			a.log.Warn("mise à jour de last_seen_at", "error", err)
		}
		next.ServeHTTP(w, r.WithContext(withUser(r.Context(), user, sess)))
	})
}

// RequireUser refuse la requête si aucune session valide n'est attachée.
// À placer derrière Resolve.
func (a *Auth) RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := UserOf(r.Context()); !ok {
			httpx.Err(w, r, http.StatusUnauthorized, httpx.CodeUnauthorized,
				"Connexion requise.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CSRF exige un jeton CSRF valide sur toute requête modifiante.
// À placer derrière RequireUser.
func (a *Auth) CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		sess, ok := SessionOf(r.Context())
		if !ok {
			httpx.Err(w, r, http.StatusUnauthorized, httpx.CodeUnauthorized, "Connexion requise.")
			return
		}
		got := r.Header.Get("X-CSRF-Token")
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(sess.CSRFToken)) != 1 {
			u, _ := UserOf(r.Context())
			a.audit(r, store.AuditEntry{
				ActorID: u.ID,
				Action:  store.ActionCSRFBlocked,
				Details: map[string]any{"path": r.URL.Path, "method": r.Method},
			})
			httpx.Err(w, r, http.StatusForbidden, httpx.CodeForbidden, "Jeton CSRF invalide.")
			return
		}
		next.ServeHTTP(w, r)
	})
}
