package api

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"spinwheel/internal/auth"
	"spinwheel/internal/httpx"
	"spinwheel/internal/imgproc"
	"spinwheel/internal/store"
	"spinwheel/internal/uid"
)

// Plus grand côté des images servies.
//
// L'image est l'élément principal d'un quartier, et l'annonce du résultat la
// montre en grand : 256 px se voyait interpolé sur un écran de classe. La
// borne reste modeste au regard du quota journalier par compte.
const imageTargetSide = 512

// Plafond d'envois par compte et par tranche de 24 heures.
//
// La limitation de débit borne la cadence instantanée ; celle-ci borne le
// volume cumulé, sans quoi le débit d'entrée dépasse structurellement celui du
// ménage horaire et le volume disque ne redescend jamais.
const maxImagesParJour = 300

// imagePath construit le chemin disque d'une image.
//
// Le nom de fichier dérive d'un UUID déjà analysé, jamais d'une chaîne fournie
// par le client : aucune traversée de répertoire n'est possible.
func (s *Server) imagePath(id uid.UUID) string {
	return filepath.Join(s.cfg.ImageDir, id.String()+".png")
}

// GET /api/images
func (s *Server) listImages(w http.ResponseWriter, r *http.Request) {
	u := auth.MustUser(r.Context())
	images, err := s.st.ListImages(r.Context(), u.ID, 200)
	if err != nil {
		s.writeError(w, r, err, "liste des images")
		return
	}
	type out struct {
		ID     string `json:"id"`
		URL    string `json:"url"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	}
	list := make([]out, 0, len(images))
	for _, img := range images {
		list = append(list, out{
			ID: img.ID.String(), URL: imageURL(img.ID),
			Width: img.Width, Height: img.Height,
		})
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"images": list})
}

// POST /api/images  (multipart/form-data, champ « image »)
func (s *Server) uploadImage(w http.ResponseWriter, r *http.Request) {
	u := auth.MustUser(r.Context())

	// Nettoyage armé avant l'analyse, par précaution seulement.
	//
	// ParseMultipartForm déverse sur disque ce qui dépasse la mémoire
	// autorisée, mais mime/multipart supprime lui-même ses fichiers
	// temporaires quand la lecture échoue, et laisse alors r.MultipartForm à
	// nil — vérifié expérimentalement : 20 corps tronqués de 2 Mio ne laissent
	// aucun fichier dans /tmp. Ce defer ne rattrape donc rien aujourd'hui ; il
	// couvre le chemin de succès et ne dépend pas de ce détail d'implémen-
	// tation de la bibliothèque standard.
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	// 1 Mio en mémoire, le reste sur /tmp (monté en tmpfs, non exécutable).
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			httpx.Err(w, r, http.StatusRequestEntityTooLarge, httpx.CodeTooLarge,
				"Fichier trop volumineux.")
			return
		}
		httpx.Err(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "Envoi illisible.")
		return
	}

	// Quota journalier par compte : la limitation de débit borne la cadence,
	// pas le volume cumulé sur une journée.
	recents, err := s.st.CountImagesSince(r.Context(), u.ID, 24*time.Hour)
	if err != nil {
		s.writeError(w, r, err, "comptage des envois récents")
		return
	}
	if recents >= maxImagesParJour {
		w.Header().Set("Retry-After", "3600")
		httpx.Err(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited,
			"Quota d'images atteint pour aujourd'hui.")
		return
	}

	file, _, err := r.FormFile("image")
	if err != nil {
		httpx.Err(w, r, http.StatusBadRequest, httpx.CodeBadRequest,
			"Champ « image » manquant.")
		return
	}
	defer file.Close()

	// Lire un octet de plus que la limite permet de détecter le dépassement
	// sans jamais charger davantage.
	raw, err := io.ReadAll(io.LimitReader(file, s.cfg.MaxImageBytes+1))
	if err != nil {
		httpx.Err(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "Lecture du fichier impossible.")
		return
	}
	if int64(len(raw)) > s.cfg.MaxImageBytes {
		httpx.Err(w, r, http.StatusRequestEntityTooLarge, httpx.CodeTooLarge,
			"Image trop volumineuse.")
		return
	}

	// Le type déclaré par le navigateur et l'extension du fichier sont
	// ignorés : seul le décodage réel du contenu fait foi.
	res, err := imgproc.Process(raw, s.cfg.MaxImagePixels, imageTargetSide)
	if err != nil {
		switch {
		case errors.Is(err, imgproc.ErrDimensions):
			httpx.Err(w, r, http.StatusBadRequest, httpx.CodeBadRequest,
				"Image trop grande (2048 px maximum par côté).")
		case errors.Is(err, imgproc.ErrFormat):
			httpx.Err(w, r, http.StatusBadRequest, httpx.CodeBadRequest,
				"Format non pris en charge. Formats acceptés : PNG, JPEG, GIF, WebP.")
		default:
			httpx.Err(w, r, http.StatusBadRequest, httpx.CodeBadRequest,
				"Fichier illisible ou corrompu.")
		}
		return
	}

	id := uid.New()
	path := s.imagePath(id)

	// Écriture atomique : un fichier temporaire dans le même dossier, puis un
	// renommage. Un incident en cours d'écriture ne laisse jamais une image
	// tronquée à l'URL publique.
	tmp := path + ".tmp"
	// Après un renommage réussi, ce Remove ne trouve plus rien et ne coûte
	// rien ; sur tout chemin d'échec, il évite de laisser un .tmp orphelin
	// qu'aucun ménage ne balaie.
	defer func() { _ = os.Remove(tmp) }()

	if err := os.WriteFile(tmp, res.PNG, 0o640); err != nil {
		s.writeError(w, r, err, "écriture de l'image")
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		s.writeError(w, r, err, "publication de l'image")
		return
	}

	img, err := s.st.CreateImage(r.Context(), id, u.ID, res.SHA256, len(res.PNG), res.Width, res.Height)
	if err != nil {
		// La ligne n'existe pas : le fichier orphelin n'a aucune raison de
		// rester sur le volume.
		_ = os.Remove(path)
		s.writeError(w, r, err, "enregistrement de l'image")
		return
	}

	s.audit(r, store.AuditEntry{
		ActorID: u.ID, Action: store.ActionImageUploaded,
		EntityType: "image", EntityID: img.ID,
		Details: map[string]any{
			"bytes":  img.ByteSize,
			"width":  img.Width,
			"height": img.Height,
		},
	})

	httpx.JSON(w, http.StatusCreated, map[string]any{
		"image": map[string]any{
			"id":     img.ID.String(),
			"url":    imageURL(img.ID),
			"width":  img.Width,
			"height": img.Height,
		},
	})
}

// GET /img/{id}
//
// Accès public : l'iframe n'a pas de session. L'identifiant UUIDv7 fait office
// de secret d'accès, comme l'URL de la roue elle-même.
func (s *Server) serveImage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(r, "id")
	if !ok {
		http.NotFound(w, r)
		return
	}

	f, err := os.Open(s.imagePath(id))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}

	h := w.Header()
	// Type imposé : le contenu a été ré-encodé en PNG par le serveur, il n'y a
	// pas d'autre possibilité. Combiné à nosniff, le navigateur ne peut pas
	// réinterpréter le fichier comme du HTML.
	h.Set("Content-Type", "image/png")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Disposition", "inline")
	// Une image est immuable : son contenu ne change jamais pour un identifiant
	// donné, un nouvel envoi crée un nouvel identifiant.
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	// Filet supplémentaire si le fichier est ouvert en navigation directe.
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox; frame-ancestors 'none'")

	http.ServeContent(w, r, "", info.ModTime(), f)
}

// PurgeOrphanImages supprime les images qu'aucun segment ne référence : la
// ligne en base et le fichier sur le volume.
//
// Un envoi abandonné en cours d'édition ne doit pas remplir le disque, mais
// une image tout juste téléversée et pas encore rattachée à un segment ne doit
// pas disparaître sous les doigts de l'utilisateur : d'où le délai de grâce.
func PurgeOrphanImages(ctx context.Context, st *store.Store, imageDir string, gracePeriod time.Duration, log *slog.Logger) int {
	ids, err := st.ListOrphanImages(ctx, gracePeriod, 200)
	if err != nil {
		log.Error("recherche des images orphelines", "error", err)
		return 0
	}

	removed := 0
	for _, id := range ids {
		// La base d'abord, et sous condition : entre la liste et la
		// suppression, l'image a pu être rattachée à un segment. La requête
		// revérifie l'absence de référence au moment du DELETE ; si elle
		// n'efface rien, le fichier doit rester.
		efface, err := st.DeleteImageIfUnused(ctx, id)
		if err != nil {
			log.Warn("suppression d'une ligne image", "error", err, "image_id", id.String())
			continue
		}
		if !efface {
			continue
		}
		path := filepath.Join(imageDir, id.String()+".png")
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			log.Warn("suppression d'un fichier image", "error", err, "image_id", id.String())
		}
		removed++
	}
	return removed
}
