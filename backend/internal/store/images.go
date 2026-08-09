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

// ListOrphanImages rend les images qu'aucune roue VIVANTE ne référence et qui
// sont assez anciennes pour ne pas être un envoi en cours d'édition.
//
// La jointure sur wheels est essentielle : SoftDeleteWheel ne fait que poser
// deleted_at, les lignes segments survivent et continuent de référencer leurs
// images. Sans cette jointure, une image d'une roue supprimée n'était jamais
// considérée comme orpheline — et comme /img/{id} sert le fichier sans
// consulter la base, la photo restait accessible indéfiniment à qui avait
// relevé son URL. Supprimer une roue est le seul levier de retrait offert à
// l'utilisateur : il doit atteindre la donnée la plus sensible du produit.
func (s *Store) ListOrphanImages(ctx context.Context, olderThan time.Duration, limit int) ([]uid.UUID, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT i.id FROM images i
		WHERE i.created_at < now() - make_interval(secs => $1)
		  AND NOT EXISTS (
		        SELECT 1 FROM segments sg
		        JOIN wheels w ON w.id = sg.wheel_id
		        WHERE sg.image_id = i.id AND w.deleted_at IS NULL)
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

// DeleteImageIfUnused supprime la ligne de métadonnées uniquement si aucune
// roue vivante ne référence l'image, et indique si la suppression a eu lieu.
//
// La condition est rejouée dans le DELETE lui-même : entre le moment où le
// ménage liste les orphelines et celui où il les efface, l'une d'elles a pu
// être rattachée à un segment. Décider en deux temps effacerait alors une
// image tout juste mise en service.
func (s *Store) DeleteImageIfUnused(ctx context.Context, id uid.UUID) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `
		DELETE FROM images i
		WHERE i.id = $1
		  AND NOT EXISTS (
		        SELECT 1 FROM segments sg
		        JOIN wheels w ON w.id = sg.wheel_id
		        WHERE sg.image_id = i.id AND w.deleted_at IS NULL)`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
