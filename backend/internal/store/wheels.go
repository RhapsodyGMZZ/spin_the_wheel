package store

import (
	"context"
	"fmt"
	"time"

	"spinwheel/internal/uid"
)

// Wheel est une roue éditable par son propriétaire.
type Wheel struct {
	ID           uid.UUID  `json:"id"`
	OwnerID      uid.UUID  `json:"-"`
	Title        string    `json:"title"`
	IsActive     bool      `json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	SegmentCount int       `json:"segment_count"`
}

// Segment est un quartier de la roue : un libellé, une couleur de fond et une
// petite image optionnelle.
type Segment struct {
	ID       uid.UUID
	Position int
	Label    string
	Color    string
	ImageID  uid.UUID // uid.Nil quand le segment n'a pas d'image
}

// Spin est un tirage enregistré.
type Spin struct {
	ID           uid.UUID  `json:"id"`
	SegmentIndex int       `json:"segment_index"`
	SegmentLabel string    `json:"segment_label"`
	CreatedAt    time.Time `json:"created_at"`
}

// --- Roues ------------------------------------------------------------------

// CreateWheel crée une roue vide.
func (s *Store) CreateWheel(ctx context.Context, ownerID uid.UUID, title string) (Wheel, error) {
	w := Wheel{ID: uid.New(), OwnerID: ownerID, Title: title, IsActive: true}
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO wheels (id, owner_id, title)
		VALUES ($1, $2, $3)
		RETURNING id, title, is_active, created_at, updated_at`,
		w.ID, ownerID, title,
	).Scan(&w.ID, &w.Title, &w.IsActive, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		return Wheel{}, fmt.Errorf("création de roue: %w", err)
	}
	return w, nil
}

// ListWheels rend les roues d'un propriétaire, la plus récente d'abord.
// L'ordre sur la clé primaire suffit : un UUIDv7 est chronologique.
func (s *Store) ListWheels(ctx context.Context, ownerID uid.UUID) ([]Wheel, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT w.id, w.title, w.is_active, w.created_at, w.updated_at,
		       (SELECT count(*) FROM segments sg WHERE sg.wheel_id = w.id)
		FROM wheels w
		WHERE w.owner_id = $1 AND w.deleted_at IS NULL
		ORDER BY w.id DESC`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Wheel{}
	for rows.Next() {
		var w Wheel
		w.OwnerID = ownerID
		if err := rows.Scan(&w.ID, &w.Title, &w.IsActive, &w.CreatedAt, &w.UpdatedAt, &w.SegmentCount); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// GetWheelOwned rend une roue à condition qu'elle appartienne au demandeur.
//
// Le filtre sur owner_id est dans la requête, pas dans une vérification
// applicative après coup : une roue d'autrui est indistinguable d'une roue
// inexistante, ce qui évite d'en révéler l'existence.
func (s *Store) GetWheelOwned(ctx context.Context, id, ownerID uid.UUID) (Wheel, error) {
	var w Wheel
	w.OwnerID = ownerID
	err := s.Pool.QueryRow(ctx, `
		SELECT id, title, is_active, created_at, updated_at
		FROM wheels
		WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL`, id, ownerID,
	).Scan(&w.ID, &w.Title, &w.IsActive, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		if isNoRows(err) {
			return Wheel{}, ErrNotFound
		}
		return Wheel{}, err
	}
	return w, nil
}

// UpdateWheel modifie le titre et l'état de publication d'une roue.
func (s *Store) UpdateWheel(ctx context.Context, id, ownerID uid.UUID, title string, isActive bool) (Wheel, error) {
	var w Wheel
	w.OwnerID = ownerID
	err := s.Pool.QueryRow(ctx, `
		UPDATE wheels
		SET title = $3, is_active = $4, updated_at = now()
		WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL
		RETURNING id, title, is_active, created_at, updated_at`,
		id, ownerID, title, isActive,
	).Scan(&w.ID, &w.Title, &w.IsActive, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		if isNoRows(err) {
			return Wheel{}, ErrNotFound
		}
		return Wheel{}, err
	}
	return w, nil
}

// SoftDeleteWheel marque une roue comme supprimée. L'historique des tirages
// reste en base.
func (s *Store) SoftDeleteWheel(ctx context.Context, id, ownerID uid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `
		UPDATE wheels SET deleted_at = now(), is_active = false, updated_at = now()
		WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL`, id, ownerID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Segments ---------------------------------------------------------------

// ListSegments rend les segments d'une roue, dans l'ordre d'affichage.
func (s *Store) ListSegments(ctx context.Context, wheelID uid.UUID) ([]Segment, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, position, label, color, COALESCE(image_id::text, '')
		FROM segments
		WHERE wheel_id = $1
		ORDER BY position ASC`, wheelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Segment{}
	for rows.Next() {
		var (
			seg     Segment
			imageID string
		)
		if err := rows.Scan(&seg.ID, &seg.Position, &seg.Label, &seg.Color, &imageID); err != nil {
			return nil, err
		}
		if imageID != "" {
			if parsed, err := uid.Parse(imageID); err == nil {
				seg.ImageID = parsed
			}
		}
		out = append(out, seg)
	}
	return out, rows.Err()
}

// ReplaceSegments remplace d'un bloc tous les segments d'une roue.
//
// Le remplacement intégral évite toute logique de différentiel côté client :
// l'éditeur envoie l'état voulu, la base l'applique en une transaction. Les
// images référencées sont vérifiées comme appartenant au propriétaire, sans
// quoi un utilisateur pourrait afficher l'image d'un autre.
func (s *Store) ReplaceSegments(ctx context.Context, wheelID, ownerID uid.UUID, segs []Segment) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op après Commit

	// Verrouille la roue et confirme la propriété dans la même transaction.
	var found uid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT id FROM wheels
		WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL
		FOR UPDATE`, wheelID, ownerID).Scan(&found); err != nil {
		if isNoRows(err) {
			return ErrNotFound
		}
		return err
	}

	if _, err := tx.Exec(ctx, `DELETE FROM segments WHERE wheel_id = $1`, wheelID); err != nil {
		return err
	}

	for i, seg := range segs {
		var imageID any
		if !seg.ImageID.IsZero() {
			// Une image d'un autre compte est refusée net.
			var ok bool
			if err := tx.QueryRow(ctx,
				`SELECT EXISTS (SELECT 1 FROM images WHERE id = $1 AND owner_id = $2)`,
				seg.ImageID, ownerID).Scan(&ok); err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("%w: image %s", ErrNotFound, seg.ImageID)
			}
			imageID = seg.ImageID
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO segments (id, wheel_id, position, label, color, image_id)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			uid.New(), wheelID, i, seg.Label, seg.Color, imageID); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(ctx,
		`UPDATE wheels SET updated_at = now() WHERE id = $1`, wheelID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// --- Lecture publique (iframe) ---------------------------------------------

// GetPublicWheel rend une roue publiée et ses segments, sans aucune donnée sur
// le propriétaire. Une roue dépubliée ou supprimée est introuvable.
func (s *Store) GetPublicWheel(ctx context.Context, id uid.UUID) (Wheel, []Segment, error) {
	var w Wheel
	err := s.Pool.QueryRow(ctx, `
		SELECT id, title, is_active, created_at, updated_at
		FROM wheels
		WHERE id = $1 AND deleted_at IS NULL AND is_active`, id,
	).Scan(&w.ID, &w.Title, &w.IsActive, &w.CreatedAt, &w.UpdatedAt)
	if err != nil {
		if isNoRows(err) {
			return Wheel{}, nil, ErrNotFound
		}
		return Wheel{}, nil, err
	}

	segs, err := s.ListSegments(ctx, id)
	if err != nil {
		return Wheel{}, nil, err
	}
	w.SegmentCount = len(segs)
	return w, segs, nil
}

// --- Tirages ----------------------------------------------------------------

// InsertSpin enregistre un tirage.
func (s *Store) InsertSpin(ctx context.Context, wheelID, segmentID uid.UUID, index int, label string, ipHash []byte, userAgent string) (uid.UUID, error) {
	id := uid.New()
	var hash any
	if len(ipHash) > 0 {
		hash = ipHash
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO spins (id, wheel_id, segment_id, segment_index, segment_label, ip_hash, user_agent)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id, wheelID, nullUUID(segmentID), index, label, hash, truncate(userAgent, 512))
	if err != nil {
		return uid.Nil, err
	}
	return id, nil
}

// ListSpins rend l'historique des tirages d'une roue, le plus récent d'abord.
func (s *Store) ListSpins(ctx context.Context, wheelID, ownerID uid.UUID, limit int) ([]Spin, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT sp.id, sp.segment_index, sp.segment_label, sp.created_at
		FROM spins sp
		JOIN wheels w ON w.id = sp.wheel_id
		WHERE sp.wheel_id = $1 AND w.owner_id = $2
		ORDER BY sp.id DESC
		LIMIT $3`, wheelID, ownerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Spin{}
	for rows.Next() {
		var sp Spin
		if err := rows.Scan(&sp.ID, &sp.SegmentIndex, &sp.SegmentLabel, &sp.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// CountSpinsSince compte les tirages récents d'une roue : sert de garde-fou
// par roue, en complément de la limitation par IP.
func (s *Store) CountSpinsSince(ctx context.Context, wheelID uid.UUID, since time.Duration) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `
		SELECT count(*) FROM spins
		WHERE wheel_id = $1 AND created_at > now() - make_interval(secs => $2)`,
		wheelID, since.Seconds()).Scan(&n)
	return n, err
}
