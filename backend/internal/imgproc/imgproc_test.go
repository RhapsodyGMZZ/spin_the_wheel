package imgproc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// pngDe fabrique un PNG opaque de la taille demandée.
func pngDe(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encodage du PNG de test: %v", err)
	}
	return buf.Bytes()
}

// enTetePNG fabrique un PNG réduit à sa signature et son en-tête IHDR. Il
// suffit à DecodeConfig, ce qui permet d'annoncer des dimensions énormes sans
// allouer l'image correspondante.
func enTetePNG(width, height uint32) []byte {
	var out bytes.Buffer
	out.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})

	data := make([]byte, 13)
	binary.BigEndian.PutUint32(data[0:4], width)
	binary.BigEndian.PutUint32(data[4:8], height)
	data[8] = 8  // profondeur de bits
	data[9] = 2  // couleur vraie (RGB)
	data[10] = 0 // compression
	data[11] = 0 // filtre
	data[12] = 0 // entrelacement

	length := make([]byte, 4)
	binary.BigEndian.PutUint32(length, uint32(len(data)))
	out.Write(length)
	out.WriteString("IHDR")
	out.Write(data)

	crc := crc32.NewIEEE()
	crc.Write([]byte("IHDR"))
	crc.Write(data)
	somme := make([]byte, 4)
	binary.BigEndian.PutUint32(somme, crc.Sum32())
	out.Write(somme)

	return out.Bytes()
}

func TestProcessRedimensionneEnConservantLesProportions(t *testing.T) {
	res, err := Process(pngDe(t, 800, 400), 2048, 256)
	if err != nil {
		t.Fatalf("Process a échoué: %v", err)
	}
	if res.Width != 256 || res.Height != 128 {
		t.Fatalf("dimensions = %dx%d, attendu 256x128", res.Width, res.Height)
	}

	cfg, format, err := image.DecodeConfig(bytes.NewReader(res.PNG))
	if err != nil {
		t.Fatalf("la sortie n'est pas une image lisible: %v", err)
	}
	if format != "png" {
		t.Fatalf("format de sortie = %q, attendu png", format)
	}
	if cfg.Width != 256 || cfg.Height != 128 {
		t.Fatalf("en-tête de sortie = %dx%d", cfg.Width, cfg.Height)
	}
	if len(res.SHA256) != 32 {
		t.Fatalf("empreinte de %d octets, attendu 32", len(res.SHA256))
	}
}

func TestProcessNAgranditPas(t *testing.T) {
	res, err := Process(pngDe(t, 40, 30), 2048, 256)
	if err != nil {
		t.Fatalf("Process a échoué: %v", err)
	}
	if res.Width != 40 || res.Height != 30 {
		t.Fatalf("dimensions = %dx%d, attendu 40x30 inchangées", res.Width, res.Height)
	}
}

func TestProcessRefuseSVG(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	if _, err := Process(svg, 2048, 256); !errors.Is(err, ErrUndecodable) {
		t.Fatalf("erreur = %v, attendu ErrUndecodable", err)
	}
}

func TestProcessRefuseFichierQuelconque(t *testing.T) {
	if _, err := Process([]byte("bonjour"), 2048, 256); !errors.Is(err, ErrUndecodable) {
		t.Fatalf("erreur = %v, attendu ErrUndecodable", err)
	}
	if _, err := Process(nil, 2048, 256); !errors.Is(err, ErrEmpty) {
		t.Fatalf("erreur = %v, attendu ErrEmpty", err)
	}
}

// Une image annoncée à 40000x40000 doit être écartée sur son en-tête, sans que
// le décodeur n'alloue jamais les pixels correspondants.
func TestProcessRefuseBombeDeDecompression(t *testing.T) {
	_, err := Process(enTetePNG(40000, 40000), 2048, 256)
	if !errors.Is(err, ErrDimensions) {
		t.Fatalf("erreur = %v, attendu ErrDimensions", err)
	}
}

// Un fichier valide comme image ET contenant du HTML ne doit pas ressortir avec
// sa charge utile : la sortie est ré-encodée à partir des pixels décodés.
func TestProcessSupprimeLaChargeAjoutee(t *testing.T) {
	charge := []byte(`<script>alert(document.domain)</script>`)
	polyglotte := append(pngDe(t, 64, 64), charge...)

	res, err := Process(polyglotte, 2048, 256)
	if err != nil {
		t.Fatalf("Process a échoué: %v", err)
	}
	if bytes.Contains(res.PNG, charge) {
		t.Fatal("la charge utile a survécu au ré-encodage")
	}
	if bytes.Contains(res.PNG, []byte("script")) {
		t.Fatal("des traces de la charge utile subsistent")
	}
}

func TestFit(t *testing.T) {
	cas := []struct {
		w, h, max  int
		outW, outH int
	}{
		{800, 400, 256, 256, 128},
		{400, 800, 256, 128, 256},
		{500, 500, 256, 256, 256},
		{100, 100, 256, 100, 100},
		{4000, 1, 256, 256, 1},
	}
	for _, c := range cas {
		w, h := fit(c.w, c.h, c.max)
		if w != c.outW || h != c.outH {
			t.Errorf("fit(%d,%d,%d) = %d,%d — attendu %d,%d",
				c.w, c.h, c.max, w, h, c.outW, c.outH)
		}
	}
}
