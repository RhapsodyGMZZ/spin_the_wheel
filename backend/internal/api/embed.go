package api

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"math/big"
	"net/http"
	"time"

	"spinwheel/internal/httpx"
	"spinwheel/internal/store"
)

// Plafond horaire de tirages pour une même roue, tous appelants confondus.
// La limitation par IP protège d'un client isolé ; ce plafond protège d'une
// campagne distribuée.
const maxSpinsPerWheelPerHour = 3000

// embedSegment est la vue publique d'un segment : aucun identifiant interne,
// aucune information sur le propriétaire.
type embedSegment struct {
	Index    int    `json:"index"`
	Label    string `json:"label"`
	Color    string `json:"color"`
	ImageURL string `json:"image_url,omitempty"`
}

// GET /api/embed/{id}
func (s *Server) getEmbedWheel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "id")
	if !ok {
		s.notFound(w, r)
		return
	}

	wheel, segs, err := s.st.GetPublicWheel(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.writeError(w, r, err, "lecture publique d'une roue")
		return
	}

	out := make([]embedSegment, 0, len(segs))
	for i, seg := range segs {
		out = append(out, embedSegment{
			Index:    i,
			Label:    seg.Label,
			Color:    seg.Color,
			ImageURL: imageURL(seg.ImageID),
		})
	}

	httpx.JSON(w, http.StatusOK, map[string]any{
		"id":       wheel.ID.String(),
		"title":    wheel.Title,
		"segments": out,
	})
}

// POST /api/embed/{id}/spin
//
// Le résultat est tiré côté serveur avec crypto/rand puis enregistré. Le
// client ne fait qu'animer la roue jusqu'à l'index reçu : ouvrir la console du
// navigateur ne permet pas de choisir le gagnant.
func (s *Server) spin(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "id")
	if !ok {
		s.notFound(w, r)
		return
	}

	wheel, segs, err := s.st.GetPublicWheel(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.writeError(w, r, err, "lecture publique d'une roue")
		return
	}
	if len(segs) < 2 {
		httpx.Err(w, r, http.StatusConflict, httpx.CodeBadRequest,
			"Cette roue n'a pas assez de segments.")
		return
	}

	recent, err := s.st.CountSpinsSince(r.Context(), wheel.ID, time.Hour)
	if err != nil {
		s.writeError(w, r, err, "comptage des tirages récents")
		return
	}
	if recent >= maxSpinsPerWheelPerHour {
		w.Header().Set("Retry-After", "300")
		httpx.Err(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited,
			"Cette roue a atteint sa limite de tirages pour l'heure.")
		return
	}

	index, err := secureIndex(len(segs))
	if err != nil {
		s.writeError(w, r, err, "tirage aléatoire")
		return
	}
	chosen := segs[index]

	spinID, err := s.st.InsertSpin(r.Context(), wheel.ID, chosen.ID, index, chosen.Label,
		s.hashIP(httpx.ClientIP(r, s.cfg.TrustProxy)), r.UserAgent())
	if err != nil {
		s.writeError(w, r, err, "enregistrement du tirage")
		return
	}

	s.audit(r, store.AuditEntry{
		Action:     store.ActionSpin,
		EntityType: "wheel",
		EntityID:   wheel.ID,
		Details: map[string]any{
			"spin_id": spinID.String(),
			"index":   index,
			"label":   chosen.Label,
		},
	})

	httpx.JSON(w, http.StatusOK, map[string]any{
		"spin_id": spinID.String(),
		"index":   index,
		"label":   chosen.Label,
		// L'annonce du résultat montre l'image en grand : le client ne doit pas
		// avoir à la retrouver dans une liste qui a pu changer entre-temps.
		"image_url": imageURL(chosen.ImageID),
	})
}

// secureIndex tire un entier uniforme dans [0, n) avec le générateur
// cryptographique du système. math/rand serait prédictible : un observateur
// pourrait deviner les tirages suivants.
func secureIndex(n int) (int, error) {
	if n <= 0 {
		return 0, errors.New("api: intervalle de tirage vide")
	}
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0, err
	}
	return int(v.Int64()), nil
}

// hashIP pseudonymise une adresse IP avant de l'enregistrer avec un tirage.
//
// Le sel est secret et propre au déploiement : le condensat reste comparable
// (deux tirages de la même IP se recoupent, utile pour détecter un abus) sans
// permettre de remonter à l'adresse par simple table arc-en-ciel.
func (s *Server) hashIP(ip string) []byte {
	if ip == "" {
		return nil
	}
	h := sha256.New()
	h.Write(s.cfg.IPHashSalt)
	h.Write([]byte(ip))
	return h.Sum(nil)
}
