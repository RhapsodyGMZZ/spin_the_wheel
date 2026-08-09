// Package imgproc valide et normalise les images téléversées.
//
// Le principe : ne jamais servir l'octet reçu. Toute image est décodée, puis
// ré-encodée en PNG par le serveur. Ce qui ressort est donc une image produite
// par Go, débarrassée des métadonnées EXIF, des profils couleur et de tout
// contenu parasite glissé après les données d'image. Un fichier polyglotte
// (valide à la fois comme image et comme HTML ou script) ne survit pas à
// l'opération.
//
// Le SVG est refusé par construction : c'est du XML, il peut porter du
// JavaScript, et aucun décodeur d'ici ne sait le lire.
package imgproc

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // enregistre le décodeur WebP
)

// Formats acceptés en entrée, tels que nommés par image.DecodeConfig.
var acceptedFormats = map[string]bool{
	"png":  true,
	"jpeg": true,
	"gif":  true,
	"webp": true,
}

// Erreurs de validation, distinguées pour produire un message utile côté
// client sans révéler de détail interne.
var (
	ErrEmpty       = errors.New("imgproc: fichier vide")
	ErrFormat      = errors.New("imgproc: format non pris en charge")
	ErrDimensions  = errors.New("imgproc: image trop grande")
	ErrUndecodable = errors.New("imgproc: image illisible")
)

// Result est l'image normalisée, prête à être écrite sur disque.
type Result struct {
	PNG    []byte
	Width  int
	Height int
	SHA256 []byte
}

// Process valide, redimensionne et ré-encode une image.
//
//	maxSourceSide : refus au-delà, avant tout décodage complet — garde-fou
//	                contre les bombes de décompression (une image annoncée
//	                40000x40000 ne sera jamais allouée).
//	targetSide    : plus grand côté de la sortie. L'image n'est jamais
//	                agrandie, seulement réduite.
func Process(raw []byte, maxSourceSide, targetSide int) (Result, error) {
	if len(raw) == 0 {
		return Result{}, ErrEmpty
	}

	// Étape 1 : lire uniquement l'en-tête. Rien n'est alloué à la taille de
	// l'image tant que ses dimensions ne sont pas jugées raisonnables.
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return Result{}, ErrUndecodable
	}
	if !acceptedFormats[format] {
		return Result{}, fmt.Errorf("%w: %s", ErrFormat, format)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return Result{}, ErrUndecodable
	}
	if cfg.Width > maxSourceSide || cfg.Height > maxSourceSide {
		return Result{}, fmt.Errorf("%w: %dx%d", ErrDimensions, cfg.Width, cfg.Height)
	}

	// Étape 2 : décodage complet.
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return Result{}, ErrUndecodable
	}

	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return Result{}, ErrUndecodable
	}

	// Étape 3 : mise à l'échelle en conservant les proportions.
	outW, outH := fit(w, h, targetSide)
	dst := image.NewRGBA(image.Rect(0, 0, outW, outH))
	// draw.Src et non draw.Over : la destination est neuve et transparente,
	// copier directement préserve le canal alpha de la source.
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)

	// Étape 4 : ré-encodage PNG.
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&buf, dst); err != nil {
		return Result{}, fmt.Errorf("imgproc: encodage PNG: %w", err)
	}

	out := buf.Bytes()
	sum := sha256.Sum256(out)
	return Result{PNG: out, Width: outW, Height: outH, SHA256: sum[:]}, nil
}

// fit calcule les dimensions de sortie pour tenir dans un carré de côté max,
// sans jamais agrandir.
func fit(w, h, max int) (int, int) {
	if max <= 0 || (w <= max && h <= max) {
		return w, h
	}
	if w >= h {
		nh := h * max / w
		if nh < 1 {
			nh = 1
		}
		return max, nh
	}
	nw := w * max / h
	if nw < 1 {
		nw = 1
	}
	return nw, max
}

// image.Decode choisit le décodeur via le registre alimenté par les paquets
// importés. Ces références gardent gif et jpeg dans le graphe de dépendances
// et documentent quels formats sont réellement acceptés en entrée.
var (
	_ = gif.Decode
	_ = jpeg.Decode
)
