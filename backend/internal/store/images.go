package store

import (
	"context"
	"time"

	"spinwheel/internal/uid"
)

// Image décrit un fichier stocké sur le volume disque. Le contenu binaire
// n'est jamais mis en base : seules les métadonnées le sont.
type Image struct {
	ID        uid.UUID  `json:"id"`
	OwnerID   uid.UUID  `json:"-"`
	ByteSize  int       `json:"byte_size"`
	Width     int       `json:"width"`
	Height    int       `json:"height"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateImage enregistre les métadonnées d'une image déjà écrite sur disque.
func (s *Store) CreateImage(ctx context.Context, id, ownerID uid.UUID, sha256 []byte, byteSize, width, height int) (Image, error) {
	img := Image{ID: id, OwnerID: ownerID, ByteSize: byteSize, Width: width, Height: height}
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO images (id, owner_id, sha256, byte_size, width, height)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at`,
		id, ownerID, sha256, byteSize, width, height).Scan(&img.CreatedAt)
	if err != nil {
		return Image{}, err
	}
	return img, nil
}

// GetImage rend les métadonnées d'une image.
func (s *Store) GetImage(ctx context.Context, id uid.UUID) (Image, error) {
	var img Image
	err := s.Pool.QueryRow(ctx, `
		SELECT id, owner_id, byte_size, width, height, created_at
		FROM images WHERE id = $1`, id,
	).Scan(&img.ID, &img.OwnerID, &img.ByteSize, &img.Width, &img.Height, &img.CreatedAt)
	if err != nil {
		if isNoRows(err) {
			return Image{}, ErrNotFound
		}
		return Image{}, err
	}
	return img, nil
}

// ListImages rend la bibliothèque d'images d'un compte, la plus récente
// d'abord.
func (s *Store) ListImages(ctx context.Context, ownerID uid.UUID, limit int) ([]Image, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT id, byte_size, width, height, created_at
		FROM images WHERE owner_id = $1
		ORDER BY id DESC LIMIT $2`, ownerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Image{}
	for rows.Next() {
		var img Image
		img.OwnerID = ownerID
		if err := rows.Scan(&img.ID, &img.ByteSize, &img.Width, &img.Height, &img.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, img)
	}
	return out, rows.Err()
}

// CountImagesSince compte les uploads récents d'un compte : garde-fou contre
// le remplissage du volume disque.
func (s *Store) CountImagesSince(ctx context.Context, ownerID uid.UUID, since time.Duration) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `
		SELECT count(*) FROM images
		WHERE owner_id = $1 AND created_at > now() - make_interval(secs => $2)`,
		ownerID, since.Seconds()).Scan(&n)
	return n, err
}

// ListOrphanImages rend les images qu'aucun segment ne référence et qui sont
// assez anciennes pour ne pas être un upload en cours d'édition.
func (s *Store) ListOrphanImages(ctx context.Context, olderThan time.Duration, limit int) ([]uid.UUID, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT i.id FROM images i
		WHERE i.created_at < now() - make_interval(secs => $1)
		  AND NOT EXISTS (SELECT 1 FROM segments s WHERE s.image_id = i.id)
		ORDER BY i.id ASC
		LIMIT $2`, olderThan.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []uid.UUID{}
	for rows.Next() {
		var id uid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// DeleteImage supprime la ligne de métadonnées.
func (s *Store) DeleteImage(ctx context.Context, id uid.UUID) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM images WHERE id = $1`, id)
	return err
}
