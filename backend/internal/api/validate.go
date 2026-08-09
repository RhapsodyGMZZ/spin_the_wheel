package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"spinwheel/internal/httpx"
	"spinwheel/internal/uid"
)

// Une couleur est un triplet hexadécimal, rien d'autre. Refuser `rgb()`,
// `var(--x)` ou les noms CSS supprime tout risque d'injection dans une
// feuille de style ou un attribut.
var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// errValidation porte un message destiné à l'utilisateur.
type errValidation struct{ msg string }

func (e errValidation) Error() string { return e.msg }

func invalid(format string, args ...any) error {
	return errValidation{msg: fmt.Sprintf(format, args...)}
}

// cleanText normalise une chaîne saisie par l'utilisateur : espaces rognés,
// caractères de contrôle et marques de direction bidirectionnelle retirés.
//
// Les caractères bidi sont retirés parce qu'ils permettent d'afficher un texte
// dans un ordre différent de celui des octets stockés — utile pour maquiller
// un libellé.
func cleanText(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		case r >= 0x202A && r <= 0x202E, // LRE, RLE, PDF, LRO, RLO
			r >= 0x2066 && r <= 0x2069, // LRI, RLI, FSI, PDI
			r == 0x200E, r == 0x200F,   // LRM, RLM
			r == 0xFEFF: // BOM
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// validText nettoie et borne une chaîne.
func validText(field, s string, minRunes, maxRunes int) (string, error) {
	if !utf8.ValidString(s) {
		return "", invalid("%s : encodage invalide.", field)
	}
	s = cleanText(s)
	n := utf8.RuneCountInString(s)
	if n < minRunes {
		if minRunes == 1 {
			return "", invalid("%s : champ obligatoire.", field)
		}
		return "", invalid("%s : %d caractères minimum.", field, minRunes)
	}
	if n > maxRunes {
		return "", invalid("%s : %d caractères maximum.", field, maxRunes)
	}
	return s, nil
}

// validColor normalise une couleur en #rrggbb minuscule.
func validColor(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !colorRe.MatchString(s) {
		return "", invalid("Couleur : format attendu #rrggbb.")
	}
	return strings.ToLower(s), nil
}

// validEmail valide et normalise une adresse.
var emailRe = regexp.MustCompile(`^[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}$`)

func validEmail(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) > 254 || !emailRe.MatchString(s) {
		return "", invalid("Adresse e-mail invalide.")
	}
	return s, nil
}

// decodeJSON lit un corps JSON strict : un seul objet, aucun champ inconnu.
//
// DisallowUnknownFields fait échouer une requête qui contient des clés non
// prévues plutôt que de les ignorer silencieusement : une faute de frappe
// devient une erreur visible au lieu d'un réglage qui ne s'applique pas.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	ct := r.Header.Get("Content-Type")
	if ct != "" {
		base, _, _ := strings.Cut(ct, ";")
		if strings.TrimSpace(base) != "application/json" {
			return invalid("Content-Type attendu : application/json.")
		}
	}

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return invalid("Corps de requête trop volumineux.")
		}
		return invalid("Corps de requête illisible.")
	}
	if dec.More() {
		return invalid("Corps de requête : un seul objet JSON attendu.")
	}
	return nil
}

// pathUUID lit un identifiant UUID dans le chemin de la requête.
func pathUUID(r *http.Request, name string) (uid.UUID, bool) {
	v, err := uid.Parse(r.PathValue(name))
	if err != nil {
		return uid.Nil, false
	}
	return v, true
}

// writeError traduit une erreur de validation en réponse 400, et toute autre
// erreur en 500 sans détail : le message interne part dans les journaux, pas
// dans le corps de la réponse.
func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error, context string) {
	var ve errValidation
	if errors.As(err, &ve) {
		httpx.Err(w, r, http.StatusBadRequest, httpx.CodeBadRequest, ve.Error())
		return
	}
	s.log.Error(context, "error", err, "request_id", httpx.RequestIDOf(r))
	httpx.Err(w, r, http.StatusInternalServerError, httpx.CodeInternal, "Erreur interne.")
}
