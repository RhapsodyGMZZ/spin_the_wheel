package store

import (
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"spinwheel/internal/uid"
)

// Toutes les requêtes du projet passent uid.UUID directement en paramètre et
// le relisent directement en résultat. Cela repose sur deux hypothèses :
// pgx sait encoder un type qui implémente driver.Valuer vers une colonne
// `uuid`, et sait y scanner un type qui implémente sql.Scanner.
//
// Ces tests vérifient les deux sans base de données, en sollicitant
// directement la table de types de pgx. Si une montée de version cassait cette
// interopérabilité, l'échec apparaîtrait ici plutôt qu'au premier INSERT en
// production.

func TestPgxEncodeUUID(t *testing.T) {
	m := pgtype.NewMap()
	id := uid.New()

	for _, format := range []struct {
		nom  string
		code int16
	}{
		{"texte", pgtype.TextFormatCode},
		{"binaire", pgtype.BinaryFormatCode},
	} {
		t.Run(format.nom, func(t *testing.T) {
			buf, err := m.Encode(pgtype.UUIDOID, format.code, id, nil)
			if err != nil {
				t.Fatalf("encodage %s refusé par pgx: %v", format.nom, err)
			}
			if len(buf) == 0 {
				t.Fatalf("encodage %s vide", format.nom)
			}
		})
	}
}

func TestPgxScanUUID(t *testing.T) {
	m := pgtype.NewMap()
	id := uid.New()

	t.Run("texte", func(t *testing.T) {
		var got uid.UUID
		if err := m.Scan(pgtype.UUIDOID, pgtype.TextFormatCode, []byte(id.String()), &got); err != nil {
			t.Fatalf("scan texte refusé par pgx: %v", err)
		}
		if got != id {
			t.Fatalf("scan texte = %v, attendu %v", got, id)
		}
	})

	t.Run("binaire", func(t *testing.T) {
		var got uid.UUID
		octets := id
		if err := m.Scan(pgtype.UUIDOID, pgtype.BinaryFormatCode, octets[:], &got); err != nil {
			t.Fatalf("scan binaire refusé par pgx: %v", err)
		}
		if got != id {
			t.Fatalf("scan binaire = %v, attendu %v", got, id)
		}
	})
}

// Une colonne uuid NULL doit se scanner en UUID nul, pas produire une erreur :
// c'est le cas de segments.image_id et audit_log.entity_id.
func TestPgxScanUUIDNull(t *testing.T) {
	m := pgtype.NewMap()

	var got uid.UUID
	if err := m.Scan(pgtype.UUIDOID, pgtype.TextFormatCode, nil, &got); err != nil {
		t.Fatalf("scan d'un NULL refusé: %v", err)
	}
	if !got.IsZero() {
		t.Fatalf("scan d'un NULL = %v, attendu l'UUID nul", got)
	}
}
