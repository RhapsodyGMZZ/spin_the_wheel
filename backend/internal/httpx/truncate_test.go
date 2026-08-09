package httpx

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Même régression que dans internal/store : cette copie sert au journal
// d'accès, qui reçoit le User-Agent et le chemin de la requête, tous deux
// entièrement contrôlés par l'appelant.
func TestTruncateNeCoupeJamaisUneRune(t *testing.T) {
	cas := []string{
		strings.Repeat("a", 255) + "é",
		strings.Repeat("é", 300),
		"/" + strings.Repeat("🎉", 100),
		strings.Repeat("a", 253) + "🎉",
	}

	for _, s := range cas {
		for n := 0; n <= len(s); n++ {
			got := truncate(s, n)
			if !utf8.ValidString(got) {
				t.Fatalf("truncate(n=%d) rend une chaîne UTF-8 invalide", n)
			}
			if !strings.HasPrefix(s, got) {
				t.Fatalf("truncate(n=%d) ne rend pas un préfixe de l'entrée", n)
			}
			if len(got) > n {
				t.Fatalf("truncate(n=%d) rend %d octets", n, len(got))
			}
		}
	}
}
