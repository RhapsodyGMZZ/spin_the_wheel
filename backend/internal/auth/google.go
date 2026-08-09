// Package auth implémente la connexion Google (OAuth 2.0 + PKCE) et les
// sessions serveur.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Points d'entrée Google. Écrits en dur : ce sont des constantes du protocole,
// et les découvrir dynamiquement ajouterait un appel réseau à chaque connexion.
const (
	googleAuthEndpoint     = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenEndpoint    = "https://oauth2.googleapis.com/token"
	googleUserInfoEndpoint = "https://openidconnect.googleapis.com/v1/userinfo"
)

// GoogleUser est le profil renvoyé par le point d'accès UserInfo.
type GoogleUser struct {
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
}

// GoogleClient parle au fournisseur d'identité.
type GoogleClient struct {
	clientID     string
	clientSecret string
	redirectURL  string
	http         *http.Client
}

// NewGoogleClient construit le client OAuth.
func NewGoogleClient(clientID, clientSecret, redirectURL string) *GoogleClient {
	return &GoogleClient{
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURL:  redirectURL,
		http: &http.Client{
			Timeout: 10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// AuthURL construit l'URL vers laquelle rediriger le navigateur.
func (c *GoogleClient) AuthURL(state, codeChallenge string) string {
	q := url.Values{}
	q.Set("client_id", c.clientID)
	q.Set("redirect_uri", c.redirectURL)
	q.Set("response_type", "code")
	q.Set("scope", "openid email profile")
	q.Set("state", state)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	// Jeton d'accès de courte durée uniquement : aucune action hors ligne au
	// nom de l'utilisateur, donc pas de refresh token à protéger.
	q.Set("access_type", "online")
	q.Set("prompt", "select_account")
	return googleAuthEndpoint + "?" + q.Encode()
}

// Exchange échange le code d'autorisation contre un jeton d'accès.
func (c *GoogleClient) Exchange(ctx context.Context, code, codeVerifier string) (string, error) {
	form := url.Values{}
	form.Set("code", code)
	form.Set("client_id", c.clientID)
	form.Set("client_secret", c.clientSecret)
	form.Set("redirect_uri", c.redirectURL)
	form.Set("grant_type", "authorization_code")
	form.Set("code_verifier", codeVerifier)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, googleTokenEndpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("appel du point de jeton: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		// Le corps peut contenir des détails sur le client OAuth : il ne
		// remonte jamais jusqu'au navigateur, seulement dans les journaux.
		return "", fmt.Errorf("échange du code refusé (HTTP %d)", resp.StatusCode)
	}

	var tok struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("réponse de jeton illisible: %w", err)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("réponse de jeton sans access_token")
	}
	return tok.AccessToken, nil
}

// UserInfo lit le profil associé à un jeton d'accès.
//
// Le profil est demandé directement à Google en TLS plutôt que déduit de
// l'id_token : pas de vérification de signature JWT à écrire, donc pas de
// vérification de signature à rater.
func (c *GoogleClient) UserInfo(ctx context.Context, accessToken string) (GoogleUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleUserInfoEndpoint, nil)
	if err != nil {
		return GoogleUser{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return GoogleUser{}, fmt.Errorf("appel de userinfo: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return GoogleUser{}, fmt.Errorf("userinfo refusé (HTTP %d)", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return GoogleUser{}, err
	}

	var u GoogleUser
	if err := json.Unmarshal(body, &u); err != nil {
		return GoogleUser{}, fmt.Errorf("profil illisible: %w", err)
	}
	if u.Sub == "" || u.Email == "" {
		return GoogleUser{}, fmt.Errorf("profil incomplet")
	}
	return u, nil
}

// --- PKCE -------------------------------------------------------------------

// newVerifier tire un vérificateur PKCE (43 caractères base64url).
func newVerifier() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// challengeOf calcule le défi S256 associé à un vérificateur.
func challengeOf(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
