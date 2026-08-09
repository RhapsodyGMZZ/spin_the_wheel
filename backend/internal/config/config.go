// Package config lit et valide la configuration passée par variables
// d'environnement. Toute valeur douteuse fait échouer le démarrage : mieux vaut
// un conteneur qui refuse de partir qu'un service en ligne avec des cookies non
// sécurisés ou une iframe ouverte à tous les domaines.
package config

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Config est la configuration résolue du serveur.
type Config struct {
	Env      string // "production" ou "development"
	LogLevel string
	HTTPAddr string

	// BaseURL est l'origine publique du site, telle que vue par le navigateur.
	BaseURL *url.URL

	DatabaseURL string

	GoogleClientID     string
	GoogleClientSecret string
	OAuthRedirectURL   string

	// BootstrapEmails alimente `allowed_emails` au premier démarrage.
	BootstrapEmails []string

	// EmbedFrameAncestors est la liste (séparée par des espaces) des origines
	// autorisées à afficher /embed/{id} dans une iframe.
	EmbedFrameAncestors string

	CookieSecure bool
	CookieName   string
	TrustProxy   bool

	IPHashSalt []byte

	ImageDir    string
	FrontendDir string

	SessionTTL    time.Duration
	MaxImageBytes int64

	// Garde-fous produits, non configurables : ils bornent la taille des
	// données acceptées et donc le coût d'une requête hostile.
	MaxSegments      int
	MaxLabelRunes    int
	MaxTitleRunes    int
	MaxImagePixels   int
	MaxJSONBodyBytes int64
}

// IsProduction indique si le service tourne en configuration de production.
func (c *Config) IsProduction() bool { return c.Env == "production" }

var (
	// Une origine de frame-ancestor : https://hote, avec joker de sous-domaine
	// (`https://*.digipad.app`), plus 'self' et localhost pour le développement.
	frameAncestorRe = regexp.MustCompile(
		`^(?:'self'` +
			`|https://(?:\*\.)?[A-Za-z0-9][A-Za-z0-9.\-]*(?::\d{1,5})?` +
			`|http://localhost(?::\d{1,5})?` +
			`|http://127\.0\.0\.1(?::\d{1,5})?)$`)

	// Validation d'adresse e-mail volontairement stricte et sans fioriture.
	emailRe = regexp.MustCompile(`^[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}$`)
)

// Load construit la configuration depuis l'environnement.
func Load() (*Config, error) {
	c := &Config{
		Env:              env("APP_ENV", "production"),
		LogLevel:         env("LOG_LEVEL", "info"),
		HTTPAddr:         env("HTTP_ADDR", ":8080"),
		ImageDir:         env("IMAGE_DIR", "/data/images"),
		FrontendDir:      env("FRONTEND_DIR", "/srv/frontend"),
		MaxSegments:      64,
		MaxLabelRunes:    80,
		MaxTitleRunes:    120,
		MaxImagePixels:   2048, // borne chaque côté : garde-fou anti-bombe de décompression
		MaxJSONBodyBytes: 256 << 10,
	}

	if c.Env != "production" && c.Env != "development" {
		return nil, fmt.Errorf("APP_ENV doit valoir production ou development (reçu %q)", c.Env)
	}

	var err error

	raw := strings.TrimRight(os.Getenv("BASE_URL"), "/")
	if raw == "" {
		return nil, fmt.Errorf("BASE_URL est obligatoire")
	}
	c.BaseURL, err = url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("BASE_URL invalide: %w", err)
	}
	if c.BaseURL.Scheme != "http" && c.BaseURL.Scheme != "https" {
		return nil, fmt.Errorf("BASE_URL doit commencer par http:// ou https://")
	}
	if c.BaseURL.Host == "" {
		return nil, fmt.Errorf("BASE_URL doit contenir un hôte")
	}
	c.BaseURL.Path, c.BaseURL.RawQuery, c.BaseURL.Fragment = "", "", ""
	c.OAuthRedirectURL = c.BaseURL.String() + "/auth/google/callback"

	if c.DatabaseURL = os.Getenv("DATABASE_URL"); c.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL est obligatoire")
	}
	if c.GoogleClientID = os.Getenv("GOOGLE_CLIENT_ID"); c.GoogleClientID == "" {
		return nil, fmt.Errorf("GOOGLE_CLIENT_ID est obligatoire")
	}
	if c.GoogleClientSecret = os.Getenv("GOOGLE_CLIENT_SECRET"); c.GoogleClientSecret == "" {
		return nil, fmt.Errorf("GOOGLE_CLIENT_SECRET est obligatoire")
	}

	salt := os.Getenv("IP_HASH_SALT")
	if len(salt) < 16 {
		return nil, fmt.Errorf("IP_HASH_SALT est obligatoire et doit faire au moins 16 caractères")
	}
	c.IPHashSalt = []byte(salt)

	if c.CookieSecure, err = envBool("COOKIE_SECURE", true); err != nil {
		return nil, err
	}
	if c.TrustProxy, err = envBool("TRUST_PROXY", true); err != nil {
		return nil, err
	}

	// Le préfixe __Host- interdit au navigateur d'accepter un cookie posé par
	// un sous-domaine ou sans Secure. Il exige Secure + Path=/ + pas de Domain.
	if c.CookieSecure {
		c.CookieName = "__Host-sw_session"
	} else {
		c.CookieName = "sw_session"
	}
	if c.IsProduction() && !c.CookieSecure {
		return nil, fmt.Errorf("COOKIE_SECURE=false est refusé quand APP_ENV=production")
	}
	if c.CookieSecure && c.BaseURL.Scheme != "https" {
		return nil, fmt.Errorf("COOKIE_SECURE=true exige une BASE_URL en https")
	}

	if c.BootstrapEmails, err = parseEmails(os.Getenv("BOOTSTRAP_ADMIN_EMAILS")); err != nil {
		return nil, err
	}

	if c.EmbedFrameAncestors, err = parseFrameAncestors(os.Getenv("EMBED_FRAME_ANCESTORS")); err != nil {
		return nil, err
	}

	hours, err := envInt("SESSION_TTL_HOURS", 168)
	if err != nil {
		return nil, err
	}
	if hours < 1 || hours > 24*90 {
		return nil, fmt.Errorf("SESSION_TTL_HOURS doit être entre 1 et 2160")
	}
	c.SessionTTL = time.Duration(hours) * time.Hour

	maxImg, err := envInt("MAX_IMAGE_BYTES", 2<<20)
	if err != nil {
		return nil, err
	}
	if maxImg < 16<<10 || maxImg > 16<<20 {
		return nil, fmt.Errorf("MAX_IMAGE_BYTES doit être entre 16384 et 16777216")
	}
	c.MaxImageBytes = int64(maxImg)

	return c, nil
}

// parseEmails découpe une liste d'e-mails séparés par des virgules, en
// normalisant la casse.
func parseEmails(raw string) ([]string, error) {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		e := strings.ToLower(strings.TrimSpace(part))
		if e == "" {
			continue
		}
		if !emailRe.MatchString(e) || len(e) > 254 {
			return nil, fmt.Errorf("BOOTSTRAP_ADMIN_EMAILS: adresse invalide %q", e)
		}
		out = append(out, e)
	}
	return out, nil
}

// parseFrameAncestors valide chaque origine autorisée à embarquer la roue.
// Le joker global `*` est refusé : autoriser n'importe quel site à encadrer la
// page ouvrirait la porte au clickjacking.
func parseFrameAncestors(raw string) (string, error) {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		// Par défaut : Digipad, la cible annoncée.
		return "https://digipad.app https://*.digipad.app", nil
	}
	for _, f := range fields {
		if f == "*" || f == "'*'" {
			return "", fmt.Errorf("EMBED_FRAME_ANCESTORS: le joker global * est refusé")
		}
		if !frameAncestorRe.MatchString(f) {
			return "", fmt.Errorf("EMBED_FRAME_ANCESTORS: origine invalide %q", f)
		}
	}
	return strings.Join(fields, " "), nil
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) (bool, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s doit valoir true ou false (reçu %q)", key, v)
	}
	return b, nil
}

func envInt(key string, def int) (int, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s doit être un entier (reçu %q)", key, v)
	}
	return n, nil
}
