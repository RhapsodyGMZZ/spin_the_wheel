package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Le quota doit mordre après la réserve initiale.
func TestLimiterBrideApresLaReserve(t *testing.T) {
	l := NewLimiter(60, 10)

	passes := 0
	for i := 0; i < 50; i++ {
		if ok, _ := l.Allow("203.0.113.9"); ok {
			passes++
		}
	}
	if passes != 10 {
		t.Fatalf("%d requêtes acceptées, attendu 10 (la réserve)", passes)
	}

	ok, attente := l.Allow("203.0.113.9")
	if ok {
		t.Fatal("une requête a été acceptée alors que le seau est vide")
	}
	if attente <= 0 {
		t.Fatal("aucun délai d'attente n'a été indiqué")
	}
}

// Deux appelants distincts ne se partagent pas le même seau.
func TestLimiterSepareLesCles(t *testing.T) {
	l := NewLimiter(60, 3)
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("la clé a aurait dû passer au tour %d", i)
		}
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("la clé a aurait dû être bridée")
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("la clé b ne doit pas subir le quota de la clé a")
	}
}

// Régression : la table des seaux ne doit pas grossir indéfiniment quand les
// clés sont nombreuses. Sans plafond, un appelant capable de faire varier la
// clé fait enfler la mémoire jusqu'au prochain passage du concierge.
func TestLimiterPlafonneLeNombreDeSeaux(t *testing.T) {
	l := NewLimiter(60, 5)
	l.max = 100 // plafond réduit pour garder le test rapide

	refus := 0
	for i := 0; i < 500; i++ {
		if ok, _ := l.Allow(string(rune('a'+i%26)) + string(rune(i))); !ok {
			refus++
		}
	}
	if taille := l.Size(); taille > 100 {
		t.Fatalf("%d seaux en mémoire, le plafond de 100 n'a pas tenu", taille)
	}
	if refus == 0 {
		t.Fatal("aucune requête refusée : le plafond n'a jamais mordu")
	}
}

// Régression : la clé de limitation du tirage ne doit pas dépendre de
// l'identifiant présent dans l'URL.
//
// Avec une clé « IP|identifiant », faire varier l'identifiant donne un seau
// neuf à chaque requête : le quota ne s'applique plus, et chaque requête
// atteint quand même la base pour se voir répondre 404.
func TestQuotaResisteAUnIdentifiantVariable(t *testing.T) {
	atteintes := 0
	fond := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atteintes++
		w.WriteHeader(http.StatusOK)
	})

	l := NewLimiter(120, 40)
	// La clé effectivement utilisée par la route de tirage.
	parIP := func(r *http.Request) string { return ClientIP(r, false) }

	mux := http.NewServeMux()
	mux.Handle("POST /api/embed/{id}/spin", Chain(fond, RateLimit(l, parIP, nil)))

	const total = 400
	for i := 0; i < total; i++ {
		// Identifiants tous différents, tous bien formés.
		id := "0199a1b2-c3d4-7e5f-8a9b-" + string([]byte{
			byte('0' + i/100%10), byte('0' + i/10%10), byte('0' + i%10),
		}) + "000000000"
		req := httptest.NewRequest(http.MethodPost, "/api/embed/"+id+"/spin", nil)
		req.RemoteAddr = "203.0.113.9:1234"
		mux.ServeHTTP(httptest.NewRecorder(), req)
	}

	if atteintes > 60 {
		t.Fatalf("%d requêtes ont atteint le gestionnaire sur %d : "+
			"le quota est contourné en faisant varier l'identifiant", atteintes, total)
	}
	if taille := l.Size(); taille != 1 {
		t.Fatalf("%d seaux créés, attendu 1 (une seule adresse IP)", taille)
	}
}
