package store

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Régression : truncate ne doit jamais produire de séquence UTF-8 invalide.
//
// Les chaînes passées à truncate viennent du client (User-Agent). Une coupure
// au milieu d'un caractère laissait un octet de continuation isolé, que
// PostgreSQL refuse à l'écriture : l'insertion du tirage échouait APRÈS le
// tirage, le client recevait 500, et la trace disparaissait du journal. Un
// en-tête calibré suffisait à le déclencher à volonté.
func TestTruncateNeCoupeJamaisUneRune(t *testing.T) {
	cas := []string{
		strings.Repeat("a", 511) + "é",
		strings.Repeat("a", 510) + "é",
		strings.Repeat("é", 400),
		strings.Repeat("🎉", 200),
		"Mozilla/5.0 " + strings.Repeat("Ω", 300),
		strings.Repeat("a", 509) + "🎉",
	}

	for _, s := range cas {
		for n := 0; n <= len(s); n++ {
			got := truncate(s, n)

			if !utf8.ValidString(got) {
				t.Fatalf("truncate(%d octets, n=%d) rend une chaîne UTF-8 invalide", len(s), n)
			}
			if !strings.HasPrefix(s, got) {
				t.Fatalf("truncate(n=%d) ne rend pas un préfixe de l'entrée", n)
			}
			if len(got) > n {
				t.Fatalf("truncate(n=%d) rend %d octets", n, len(got))
			}
			// La coupure ne doit pas être plus agressive que nécessaire :
			// au plus 3 octets sacrifiés, la taille maximale d'un préfixe de
			// rune incomplet en UTF-8.
			if len(s) > n && n-len(got) > 3 {
				t.Fatalf("truncate(n=%d) a sacrifié %d octets", n, n-len(got))
			}
		}
	}
}

func TestTruncateLaisseIntactCeQuiTient(t *testing.T) {
	s := "Café ☕"
	if got := truncate(s, 512); got != s {
		t.Fatalf("truncate a modifié une chaîne plus courte que la borne : %q", got)
	}
}
