package uid

import (
	"bytes"
	"sort"
	"testing"
	"time"
)

func TestNewIsVersion7(t *testing.T) {
	u := New()
	if got := u.Version(); got != 7 {
		t.Fatalf("version = %d, attendu 7", got)
	}
	if u[8]&0xC0 != 0x80 {
		t.Fatalf("variante RFC 4122 absente: %#x", u[8])
	}
}

func TestTimeRoundTrip(t *testing.T) {
	want := time.Date(2026, 8, 9, 14, 30, 15, 123_000_000, time.UTC)
	got := NewAt(want).Time()
	if !got.Equal(want) {
		t.Fatalf("Time() = %s, attendu %s", got, want)
	}
}

func TestTimeIsNow(t *testing.T) {
	before := time.Now().Add(-time.Second)
	u := New()
	after := time.Now().Add(time.Second)

	ts := u.Time()
	if ts.Before(before) || ts.After(after) {
		t.Fatalf("horodatage hors de l'intervalle: %s", ts)
	}
}

func TestStringParseRoundTrip(t *testing.T) {
	u := New()
	s := u.String()

	if len(s) != 36 {
		t.Fatalf("longueur = %d, attendu 36", len(s))
	}
	back, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q) a échoué: %v", s, err)
	}
	if back != u {
		t.Fatalf("aller-retour cassé: %v != %v", back, u)
	}
}

func TestParseRejette(t *testing.T) {
	cas := []string{
		"",
		"pas-un-uuid",
		"0189d6e4b7f37c8e9a1b2c3d4e5f6071",       // sans tirets
		"{0189d6e4-b7f3-7c8e-9a1b-2c3d4e5f6071}", // avec accolades
		"0189d6e4-b7f3-7c8e-9a1b-2c3d4e5f60zz",   // hexadécimal invalide
		"0189d6e4-b7f3-7c8e-9a1b-2c3d4e5f6071-extra", // trop long
		"../../../etc/passwd",                        // tentative de traversée
	}
	for _, s := range cas {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) aurait dû échouer", s)
		}
	}
}

// Le tri lexicographique des UUIDv7 doit suivre l'ordre chronologique : c'est
// la propriété sur laquelle reposent tous les ORDER BY id du projet.
func TestOrdreChronologique(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	ids := make([]UUID, 0, 50)
	for i := 0; i < 50; i++ {
		ids = append(ids, NewAt(base.Add(time.Duration(i)*time.Millisecond)))
	}

	melange := make([]UUID, len(ids))
	copy(melange, ids)
	sort.Slice(melange, func(i, j int) bool {
		return bytes.Compare(melange[i][:], melange[j][:]) < 0
	})

	for i := range ids {
		if melange[i] != ids[i] {
			t.Fatalf("position %d : le tri binaire ne suit pas l'ordre de création", i)
		}
	}
}

func TestScanValue(t *testing.T) {
	u := New()

	v, err := u.Value()
	if err != nil {
		t.Fatalf("Value() a échoué: %v", err)
	}
	s, ok := v.(string)
	if !ok {
		t.Fatalf("Value() a rendu %T, attendu string", v)
	}

	var fromString UUID
	if err := fromString.Scan(s); err != nil {
		t.Fatalf("Scan(string) a échoué: %v", err)
	}
	if fromString != u {
		t.Fatal("Scan(string) n'a pas restitué l'identifiant")
	}

	var fromBytes UUID
	if err := fromBytes.Scan(u[:]); err != nil {
		t.Fatalf("Scan([]byte 16) a échoué: %v", err)
	}
	if fromBytes != u {
		t.Fatal("Scan([]byte 16) n'a pas restitué l'identifiant")
	}

	var fromNil UUID
	if err := fromNil.Scan(nil); err != nil {
		t.Fatalf("Scan(nil) a échoué: %v", err)
	}
	if !fromNil.IsZero() {
		t.Fatal("Scan(nil) aurait dû produire l'UUID nul")
	}
}
