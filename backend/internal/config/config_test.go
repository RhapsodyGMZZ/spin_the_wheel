package config

import (
	"strings"
	"testing"
)

func TestParseFrameAncestorsAccepte(t *testing.T) {
	cas := map[string]string{
		"https://digipad.app":                       "https://digipad.app",
		"https://digipad.app https://*.digipad.app": "https://digipad.app https://*.digipad.app",
		"'self'":                       "'self'",
		"'self' http://localhost:8080": "'self' http://localhost:8080",
		"https://ent.example.fr:8443":  "https://ent.example.fr:8443",
	}
	for entree, attendu := range cas {
		got, err := parseFrameAncestors(entree)
		if err != nil {
			t.Errorf("parseFrameAncestors(%q) a échoué: %v", entree, err)
			continue
		}
		if got != attendu {
			t.Errorf("parseFrameAncestors(%q) = %q, attendu %q", entree, got, attendu)
		}
	}
}

func TestParseFrameAncestorsDefaut(t *testing.T) {
	got, err := parseFrameAncestors("   ")
	if err != nil {
		t.Fatalf("valeur vide refusée: %v", err)
	}
	if !strings.Contains(got, "digipad.app") {
		t.Fatalf("le défaut devrait viser Digipad, obtenu %q", got)
	}
}

func TestParseFrameAncestorsRefuse(t *testing.T) {
	cas := []string{
		"*",                         // joker global : clickjacking ouvert
		"'*'",                       //
		"http://exemple.fr",         // http hors localhost
		"javascript:alert(1)",       //
		"https://exemple.fr/chemin", // un chemin n'est pas une origine
		"data:",                     //
		"'unsafe-inline'",           //
	}
	for _, entree := range cas {
		if _, err := parseFrameAncestors(entree); err == nil {
			t.Errorf("parseFrameAncestors(%q) aurait dû échouer", entree)
		}
	}
}

func TestParseEmails(t *testing.T) {
	got, err := parseEmails(" Nicolas.Legay@HAFA.fr , autre@exemple.fr ")
	if err != nil {
		t.Fatalf("échec inattendu: %v", err)
	}
	if len(got) != 2 || got[0] != "nicolas.legay@hafa.fr" || got[1] != "autre@exemple.fr" {
		t.Fatalf("normalisation incorrecte: %#v", got)
	}

	if _, err := parseEmails("pas-une-adresse"); err == nil {
		t.Fatal("une adresse invalide aurait dû être refusée")
	}
}

// Configuration minimale valide, réutilisée par les tests de Load.
func envValide(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "production")
	t.Setenv("BASE_URL", "https://wheel.nicolas-legay.fr/")
	t.Setenv("DATABASE_URL", "postgres://u:p@db:5432/spinwheel")
	t.Setenv("GOOGLE_CLIENT_ID", "client")
	t.Setenv("GOOGLE_CLIENT_SECRET", "secret")
	t.Setenv("IP_HASH_SALT", "0123456789abcdef0123456789abcdef")
	t.Setenv("COOKIE_SECURE", "true")
	t.Setenv("EMBED_FRAME_ANCESTORS", "https://digipad.app https://*.digipad.app")
	t.Setenv("BOOTSTRAP_ADMIN_EMAILS", "")
	t.Setenv("SESSION_TTL_HOURS", "")
	t.Setenv("MAX_IMAGE_BYTES", "")
}

func TestLoad(t *testing.T) {
	envValide(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load a échoué: %v", err)
	}

	// Le slash final doit disparaître, sinon l'URI de redirection ne
	// correspondrait plus à celle déclarée chez Google.
	if cfg.BaseURL.String() != "https://wheel.nicolas-legay.fr" {
		t.Errorf("BaseURL = %q", cfg.BaseURL.String())
	}
	if cfg.OAuthRedirectURL != "https://wheel.nicolas-legay.fr/auth/google/callback" {
		t.Errorf("OAuthRedirectURL = %q", cfg.OAuthRedirectURL)
	}
	if cfg.CookieName != "__Host-sw_session" {
		t.Errorf("CookieName = %q, attendu le préfixe __Host-", cfg.CookieName)
	}
	if cfg.SessionTTL.Hours() != 168 {
		t.Errorf("SessionTTL = %v", cfg.SessionTTL)
	}
}

func TestLoadRefuseCookieNonSecuriseEnProduction(t *testing.T) {
	envValide(t)
	t.Setenv("COOKIE_SECURE", "false")

	if _, err := Load(); err == nil {
		t.Fatal("COOKIE_SECURE=false en production aurait dû être refusé")
	}
}

func TestLoadRefuseHTTPSSansSecure(t *testing.T) {
	envValide(t)
	t.Setenv("BASE_URL", "http://wheel.nicolas-legay.fr")

	if _, err := Load(); err == nil {
		t.Fatal("COOKIE_SECURE=true avec une BASE_URL en http aurait dû être refusé")
	}
}

func TestLoadRefuseSelSansEntropie(t *testing.T) {
	envValide(t)
	t.Setenv("IP_HASH_SALT", "court")

	if _, err := Load(); err == nil {
		t.Fatal("un sel trop court aurait dû être refusé")
	}
}

func TestLoadDeveloppementAutoriseLocalhost(t *testing.T) {
	envValide(t)
	t.Setenv("APP_ENV", "development")
	t.Setenv("BASE_URL", "http://localhost:8080")
	t.Setenv("COOKIE_SECURE", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load a échoué: %v", err)
	}
	if cfg.CookieName != "sw_session" {
		t.Errorf("CookieName = %q, attendu sans préfixe __Host-", cfg.CookieName)
	}
}
