package api

import (
	"strings"
	"testing"
)

func TestValidColor(t *testing.T) {
	valides := map[string]string{
		"#FF0000":   "#ff0000",
		"#0f172a":   "#0f172a",
		" #ABCdef ": "#abcdef",
	}
	for entree, attendu := range valides {
		got, err := validColor(entree)
		if err != nil {
			t.Errorf("validColor(%q) a échoué: %v", entree, err)
			continue
		}
		if got != attendu {
			t.Errorf("validColor(%q) = %q, attendu %q", entree, got, attendu)
		}
	}

	invalides := []string{
		"",
		"rouge",
		"#fff",
		"#gggggg",
		"rgb(255,0,0)",
		"var(--x)",
		"#ff0000; background:url(javascript:alert(1))",
		"expression(alert(1))",
	}
	for _, entree := range invalides {
		if _, err := validColor(entree); err == nil {
			t.Errorf("validColor(%q) aurait dû échouer", entree)
		}
	}
}

func TestCleanTextRetireLesCaracteresDangereux(t *testing.T) {
	// U+0000 (nul) et U+202E (RIGHT-TO-LEFT OVERRIDE, qui réordonne
	// l'affichage sans changer les octets stockés — de quoi maquiller un
	// libellé) doivent disparaître.
	got := cleanText("  Lot\x00 n°1‮ ABC \t ")

	if strings.ContainsRune(got, 0) {
		t.Fatal("un caractère nul a survécu")
	}
	if strings.ContainsRune(got, 0x202E) {
		t.Fatal("une marque bidirectionnelle a survécu")
	}
	if strings.HasPrefix(got, " ") || strings.HasSuffix(got, " ") {
		t.Fatalf("espaces non rognés: %q", got)
	}
	if !strings.Contains(got, "Lot") || !strings.Contains(got, "ABC") {
		t.Fatalf("le texte utile a été perdu: %q", got)
	}
}

func TestCleanTextGardeLeTexteLegitime(t *testing.T) {
	entree := "Café — 1ᵉʳ prix 🎉"
	if got := cleanText(entree); got != entree {
		t.Fatalf("cleanText a altéré un libellé valide: %q → %q", entree, got)
	}
}

// Le balisage n'est pas filtré : il est conservé tel quel puis peint sur le
// canvas. Ce test verrouille ce choix — si un jour un libellé passait par le
// DOM, il faudrait revoir la stratégie de rendu, pas ajouter un filtre ici.
func TestValidTextConserveLeBalisageTelQuel(t *testing.T) {
	entree := `<script>alert(1)</script>`
	got, err := validText("Libellé", entree, 1, 80)
	if err != nil {
		t.Fatalf("validText a échoué: %v", err)
	}
	if got != entree {
		t.Fatalf("validText = %q, attendu la chaîne inchangée", got)
	}
}

func TestValidTextBornes(t *testing.T) {
	if _, err := validText("Titre", "   ", 1, 80); err == nil {
		t.Error("une chaîne vide après nettoyage aurait dû être refusée")
	}
	if _, err := validText("Titre", strings.Repeat("a", 81), 1, 80); err == nil {
		t.Error("une chaîne trop longue aurait dû être refusée")
	}
	// La borne compte des caractères, pas des octets : 80 caractères
	// accentués font 160 octets et doivent passer.
	if _, err := validText("Titre", strings.Repeat("é", 80), 1, 80); err != nil {
		t.Errorf("80 caractères accentués refusés: %v", err)
	}
	if _, err := validText("Note", "", 0, 200); err != nil {
		t.Errorf("une note vide devrait être acceptée: %v", err)
	}
}

func TestValidEmail(t *testing.T) {
	got, err := validEmail("  Nicolas.Legay@HAFA.fr ")
	if err != nil {
		t.Fatalf("validEmail a échoué: %v", err)
	}
	if got != "nicolas.legay@hafa.fr" {
		t.Fatalf("validEmail = %q", got)
	}

	invalides := []string{"", "pas-une-adresse", "a@b", "@exemple.fr", "a b@exemple.fr"}
	for _, entree := range invalides {
		if _, err := validEmail(entree); err == nil {
			t.Errorf("validEmail(%q) aurait dû échouer", entree)
		}
	}
}

func TestItoa(t *testing.T) {
	cas := map[int]string{0: "0", 1: "1", 9: "9", 42: "42", 1234: "1234"}
	for entree, attendu := range cas {
		if got := itoa(entree); got != attendu {
			t.Errorf("itoa(%d) = %q, attendu %q", entree, got, attendu)
		}
	}
}
