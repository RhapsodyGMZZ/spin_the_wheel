package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"time"

	"spinwheel/internal/store"
)

type ctxKey int

const (
	ctxKeyUser ctxKey = iota
	ctxKeySession
)

// newToken tire un jeton opaque de 32 octets, encodé en base64url.
func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken réduit un jeton à son SHA-256. Seul le condensat est stocké : une
// copie de la base ne permet pas de fabriquer un cookie valide.
func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// setCookie pose un cookie de session ou d'état OAuth.
func (a *Auth) setCookie(w http.ResponseWriter, name, value string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:  name,
		Value: value,
		Path:  "/",
		// Lax laisse passer le cookie au retour de la redirection Google
		// (navigation de premier niveau en GET) tout en le retenant sur les
		// requêtes déclenchées par un site tiers.
		SameSite: http.SameSiteLaxMode,
		HttpOnly: true,
		Secure:   a.cfg.CookieSecure,
		MaxAge:   int(ttl.Seconds()),
	})
}

// clearCookie efface un cookie côté navigateur.
func (a *Auth) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
		HttpOnly: true,
		Secure:   a.cfg.CookieSecure,
		MaxAge:   -1,
	})
}

// oauthCookieName rend le nom du cookie d'état OAuth.
func (a *Auth) oauthCookieName() string {
	if a.cfg.CookieSecure {
		return "__Host-sw_oauth"
	}
	return "sw_oauth"
}

// --- Contexte ---------------------------------------------------------------

// withUser attache le compte et la session au contexte.
func withUser(ctx context.Context, u store.User, s store.Session) context.Context {
	ctx = context.WithValue(ctx, ctxKeyUser, u)
	return context.WithValue(ctx, ctxKeySession, s)
}

// UserOf rend le compte connecté attaché à la requête.
func UserOf(ctx context.Context) (store.User, bool) {
	u, ok := ctx.Value(ctxKeyUser).(store.User)
	return u, ok
}

// SessionOf rend la session attachée à la requête.
func SessionOf(ctx context.Context) (store.Session, bool) {
	s, ok := ctx.Value(ctxKeySession).(store.Session)
	return s, ok
}

// MustUser rend le compte connecté. À n'appeler que derrière RequireUser.
func MustUser(ctx context.Context) store.User {
	u, ok := UserOf(ctx)
	if !ok {
		panic("auth: aucun compte dans le contexte — RequireUser manquant sur la route")
	}
	return u
}
