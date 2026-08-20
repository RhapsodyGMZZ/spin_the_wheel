package api

import (
	"errors"
	"net/http"

	"spinwheel/internal/auth"
	"spinwheel/internal/httpx"
	"spinwheel/internal/store"
	"spinwheel/internal/uid"
)

// segmentOutput est la forme d'un segment renvoyée à l'éditeur.
type segmentOutput struct {
	ID       string `json:"id"`
	Position int    `json:"position"`
	Label    string `json:"label"`
	Color    string `json:"color"`
	ImageID  string `json:"image_id,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

// imageURL construit l'URL publique d'une image.
func imageURL(id uid.UUID) string {
	if id.IsZero() {
		return ""
	}
	return "/img/" + id.String()
}

func toSegmentOutputs(segs []store.Segment) []segmentOutput {
	out := make([]segmentOutput, 0, len(segs))
	for _, s := range segs {
		o := segmentOutput{
			ID:       s.ID.String(),
			Position: s.Position,
			Label:    s.Label,
			Color:    s.Color,
		}
		if !s.ImageID.IsZero() {
			o.ImageID = s.ImageID.String()
			o.ImageURL = imageURL(s.ImageID)
		}
		out = append(out, o)
	}
	return out
}

// notFound rend la même réponse pour « n'existe pas » et « ne vous appartient
// pas » : rien ne permet de sonder l'existence des roues d'autrui.
func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	httpx.Err(w, r, http.StatusNotFound, httpx.CodeNotFound, "Ressource introuvable.")
}

// GET /api/wheels
func (s *Server) listWheels(w http.ResponseWriter, r *http.Request) {
	u := auth.MustUser(r.Context())
	wheels, err := s.st.ListWheels(r.Context(), u.ID)
	if err != nil {
		s.writeError(w, r, err, "liste des roues")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"wheels": wheels})
}

type createWheelInput struct {
	Title string `json:"title"`
}

// POST /api/wheels
func (s *Server) createWheel(w http.ResponseWriter, r *http.Request) {
	u := auth.MustUser(r.Context())

	var in createWheelInput
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err, "")
		return
	}
	title, err := validText("Titre", in.Title, 1, s.cfg.MaxTitleRunes)
	if err != nil {
		s.writeError(w, r, err, "")
		return
	}

	wheel, err := s.st.CreateWheel(r.Context(), u.ID, title)
	if err != nil {
		s.writeError(w, r, err, "création de roue")
		return
	}

	s.audit(r, store.AuditEntry{
		ActorID: u.ID, Action: store.ActionWheelCreated,
		EntityType: "wheel", EntityID: wheel.ID,
		Details: map[string]any{"title": wheel.Title},
	})
	httpx.JSON(w, http.StatusCreated, map[string]any{"wheel": wheel})
}

// GET /api/wheels/{id}
func (s *Server) getWheel(w http.ResponseWriter, r *http.Request) {
	u := auth.MustUser(r.Context())
	id, ok := pathUUID(r, "id")
	if !ok {
		s.notFound(w, r)
		return
	}

	wheel, err := s.st.GetWheelOwned(r.Context(), id, u.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.writeError(w, r, err, "lecture d'une roue")
		return
	}
	segs, err := s.st.ListSegments(r.Context(), id)
	if err != nil {
		s.writeError(w, r, err, "lecture des segments")
		return
	}
	wheel.SegmentCount = len(segs)

	httpx.JSON(w, http.StatusOK, map[string]any{
		"wheel":     wheel,
		"segments":  toSegmentOutputs(segs),
		"embed_url": s.cfg.BaseURL.String() + "/embed/" + wheel.ID.String(),
	})
}

type patchWheelInput struct {
	Title    *string `json:"title"`
	IsActive *bool   `json:"is_active"`
}

// PATCH /api/wheels/{id}
func (s *Server) patchWheel(w http.ResponseWriter, r *http.Request) {
	u := auth.MustUser(r.Context())
	id, ok := pathUUID(r, "id")
	if !ok {
		s.notFound(w, r)
		return
	}

	var in patchWheelInput
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err, "")
		return
	}

	current, err := s.st.GetWheelOwned(r.Context(), id, u.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.writeError(w, r, err, "lecture d'une roue")
		return
	}

	title := current.Title
	if in.Title != nil {
		if title, err = validText("Titre", *in.Title, 1, s.cfg.MaxTitleRunes); err != nil {
			s.writeError(w, r, err, "")
			return
		}
	}
	isActive := current.IsActive
	if in.IsActive != nil {
		isActive = *in.IsActive
	}

	updated, err := s.st.UpdateWheel(r.Context(), id, u.ID, title, isActive)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.writeError(w, r, err, "mise à jour d'une roue")
		return
	}

	s.audit(r, store.AuditEntry{
		ActorID: u.ID, Action: store.ActionWheelUpdated,
		EntityType: "wheel", EntityID: id,
		Details: map[string]any{"title": updated.Title, "is_active": updated.IsActive},
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"wheel": updated})
}

// DELETE /api/wheels/{id}
func (s *Server) deleteWheel(w http.ResponseWriter, r *http.Request) {
	u := auth.MustUser(r.Context())
	id, ok := pathUUID(r, "id")
	if !ok {
		s.notFound(w, r)
		return
	}

	if err := s.st.SoftDeleteWheel(r.Context(), id, u.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.writeError(w, r, err, "suppression d'une roue")
		return
	}

	s.audit(r, store.AuditEntry{
		ActorID: u.ID, Action: store.ActionWheelDeleted,
		EntityType: "wheel", EntityID: id,
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

type segmentInput struct {
	Label   string `json:"label"`
	Color   string `json:"color"`
	ImageID string `json:"image_id"`
}

type putSegmentsInput struct {
	Segments []segmentInput `json:"segments"`
}

// PUT /api/wheels/{id}/segments
//
// Remplacement intégral : le client envoie l'état voulu de la roue, le serveur
// l'applique en une transaction. Pas de différentiel à synchroniser.
func (s *Server) putSegments(w http.ResponseWriter, r *http.Request) {
	u := auth.MustUser(r.Context())
	id, ok := pathUUID(r, "id")
	if !ok {
		s.notFound(w, r)
		return
	}

	var in putSegmentsInput
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err, "")
		return
	}
	if len(in.Segments) < 2 {
		s.writeError(w, r, invalid("Une roue demande au moins 2 segments."), "")
		return
	}
	if len(in.Segments) > s.cfg.MaxSegments {
		s.writeError(w, r, invalid("Une roue accepte au plus %d segments.", s.cfg.MaxSegments), "")
		return
	}

	segs := make([]store.Segment, 0, len(in.Segments))
	for i, raw := range in.Segments {
		label, err := validText("Libellé du segment "+itoa(i+1), raw.Label, 0, s.cfg.MaxLabelRunes)
		if err != nil {
			s.writeError(w, r, err, "")
			return
		}
		color, err := validColor(raw.Color)
		if err != nil {
			s.writeError(w, r, err, "")
			return
		}
		seg := store.Segment{Position: i, Label: label, Color: color}
		if raw.ImageID != "" {
			imgID, err := uid.Parse(raw.ImageID)
			if err != nil {
				s.writeError(w, r, invalid("Segment %d : identifiant d'image invalide.", i+1), "")
				return
			}
			seg.ImageID = imgID
		}

		// Le libellé est facultatif, l'image aussi — mais pas les deux à la
		// fois : un quartier sans rien à montrer ne serait qu'un aplat de
		// couleur, et l'annonce du tirage n'aurait rien à afficher.
		if label == "" && seg.ImageID.IsZero() {
			s.writeError(w, r, invalid("Segment %d : ajoutez un libellé ou une image.", i+1), "")
			return
		}
		segs = append(segs, seg)
	}

	if err := s.st.ReplaceSegments(r.Context(), id, u.ID, segs); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.writeError(w, r, err, "enregistrement des segments")
		return
	}

	saved, err := s.st.ListSegments(r.Context(), id)
	if err != nil {
		s.writeError(w, r, err, "relecture des segments")
		return
	}

	s.audit(r, store.AuditEntry{
		ActorID: u.ID, Action: store.ActionSegmentsSaved,
		EntityType: "wheel", EntityID: id,
		Details: map[string]any{"count": len(saved)},
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"segments": toSegmentOutputs(saved)})
}

// GET /api/wheels/{id}/spins
func (s *Server) listSpins(w http.ResponseWriter, r *http.Request) {
	u := auth.MustUser(r.Context())
	id, ok := pathUUID(r, "id")
	if !ok {
		s.notFound(w, r)
		return
	}
	spins, err := s.st.ListSpins(r.Context(), id, u.ID, 100)
	if err != nil {
		s.writeError(w, r, err, "historique des tirages")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"spins": spins})
}

// --- Liste blanche ----------------------------------------------------------

// GET /api/allowed-emails
func (s *Server) listAllowed(w http.ResponseWriter, r *http.Request) {
	emails, err := s.st.ListAllowedEmails(r.Context())
	if err != nil {
		s.writeError(w, r, err, "liste blanche")
		return
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"allowed_emails": emails})
}

type addAllowedInput struct {
	Email string `json:"email"`
	Note  string `json:"note"`
}

// POST /api/allowed-emails
//
// Tout compte connecté peut inviter : la liste blanche est plate, sans rôle
// administrateur. C'est un choix assumé pour un outil interne à effectif
// réduit ; l'ajout et le retrait sont tracés dans le journal d'audit.
func (s *Server) addAllowed(w http.ResponseWriter, r *http.Request) {
	u := auth.MustUser(r.Context())

	var in addAllowedInput
	if err := decodeJSON(w, r, &in); err != nil {
		s.writeError(w, r, err, "")
		return
	}
	email, err := validEmail(in.Email)
	if err != nil {
		s.writeError(w, r, err, "")
		return
	}
	note, err := validText("Note", in.Note, 0, 200)
	if err != nil {
		s.writeError(w, r, err, "")
		return
	}

	entry, err := s.st.AddAllowedEmail(r.Context(), email, note, u.ID)
	if err != nil {
		s.writeError(w, r, err, "ajout à la liste blanche")
		return
	}

	s.audit(r, store.AuditEntry{
		ActorID: u.ID, Action: store.ActionAllowedEmailAdded,
		EntityType: "allowed_email", EntityID: entry.ID,
		Details: map[string]any{"email": email},
	})
	httpx.JSON(w, http.StatusCreated, map[string]any{"allowed_email": entry})
}

// DELETE /api/allowed-emails/{id}
func (s *Server) deleteAllowed(w http.ResponseWriter, r *http.Request) {
	u := auth.MustUser(r.Context())
	id, ok := pathUUID(r, "id")
	if !ok {
		s.notFound(w, r)
		return
	}

	email, err := s.st.DeleteAllowedEmail(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.writeError(w, r, err, "retrait de la liste blanche")
		return
	}

	s.audit(r, store.AuditEntry{
		ActorID: u.ID, Action: store.ActionAllowedEmailRemoved,
		EntityType: "allowed_email", EntityID: id,
		Details: map[string]any{"email": email},
	})
	httpx.JSON(w, http.StatusOK, map[string]any{"ok": true})
}

// itoa évite d'importer strconv pour une seule conversion dans un message.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
