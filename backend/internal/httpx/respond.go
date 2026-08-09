// Package httpx contient les briques HTTP transverses : réponses JSON,
// intergiciels de sécurité, limitation de débit, extraction de l'IP client.
package httpx

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
)

// Codes d'erreur exposés au client. Volontairement génériques : ils
// n'indiquent jamais si une ressource existe mais appartient à quelqu'un
// d'autre.
const (
	CodeBadRequest   = "requete_invalide"
	CodeUnauthorized = "non_authentifie"
	CodeForbidden    = "interdit"
	CodeNotFound     = "introuvable"
	CodeTooLarge     = "trop_volumineux"
	CodeRateLimited  = "trop_de_requetes"
	CodeInternal     = "erreur_interne"
)

type errorBody struct {
	Error errorPayload `json:"error"`
}

type errorPayload struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

// JSON écrit une réponse JSON.
//
// Les en-têtes anti-sniffing et anti-cache sont posés systématiquement : une
// réponse d'API ne doit jamais être interprétée comme du HTML par le
// navigateur, ni rester dans un cache intermédiaire.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	enc := json.NewEncoder(w)
	// Pas d'échappement HTML supplémentaire : le corps n'est jamais injecté
	// dans du HTML, et le front n'utilise que textContent.
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}

// Err écrit une erreur JSON. Le message doit rester générique : aucun détail
// interne (requête SQL, chemin de fichier, adresse) ne sort d'ici.
func Err(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	JSON(w, status, errorBody{Error: errorPayload{
		Code:      code,
		Message:   message,
		RequestID: RequestIDOf(r),
	}})
}

// RequestIDOf relit l'identifiant de requête posé par l'intergiciel.
func RequestIDOf(r *http.Request) string {
	if r == nil {
		return ""
	}
	if v, ok := r.Context().Value(ctxKeyRequestID).(string); ok {
		return v
	}
	return ""
}

// ClientIP résout l'adresse de l'appelant.
//
// Les en-têtes X-Real-IP / X-Forwarded-For ne sont lus que si trustProxy est
// vrai, c'est-à-dire uniquement quand le service n'est joignable qu'à travers
// nginx. Sans cette condition, n'importe qui pourrait usurper son IP et
// contourner la limitation de débit.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if v := strings.TrimSpace(r.Header.Get("X-Real-IP")); v != "" {
			if ip := net.ParseIP(v); ip != nil {
				return ip.String()
			}
		}
		if v := r.Header.Get("X-Forwarded-For"); v != "" {
			first, _, _ := strings.Cut(v, ",")
			if ip := net.ParseIP(strings.TrimSpace(first)); ip != nil {
				return ip.String()
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
