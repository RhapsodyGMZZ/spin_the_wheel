// Package uid implémente UUIDv7 (RFC 9562) sans dépendance externe.
//
// Un UUIDv7 porte, dans ses 48 bits de poids fort, un horodatage Unix en
// millisecondes. C'est ce qui en fait l'identifiant interne du projet : toute
// ligne insérée est datée et triable chronologiquement par sa clé primaire,
// sans jointure ni colonne supplémentaire.
//
//	 0                   1                   2                   3
//	 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|                          unix_ts_ms                           |
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|          unix_ts_ms           |  ver  |        rand_a         |
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|var|                        rand_b                             |
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
//	|                            rand_b                             |
//	+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
package uid

import (
	"crypto/rand"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// UUID est un UUID sur 16 octets, en ordre réseau (big endian).
type UUID [16]byte

// Nil est l'UUID nul (00000000-0000-0000-0000-000000000000).
var Nil UUID

// ErrInvalid est renvoyée quand une chaîne n'est pas un UUID canonique.
var ErrInvalid = errors.New("uid: UUID invalide")

// New génère un UUIDv7 basé sur l'heure courante.
//
// Panique si le générateur d'aléa du système est indisponible : continuer avec
// des identifiants prédictibles serait pire qu'un arrêt franc.
func New() UUID {
	return NewAt(time.Now())
}

// NewAt génère un UUIDv7 portant l'horodatage fourni.
func NewAt(t time.Time) UUID {
	var u UUID

	// 74 bits d'aléa (les bits de version et de variante sont réécrits ensuite).
	if _, err := rand.Read(u[6:]); err != nil {
		panic("uid: crypto/rand indisponible: " + err.Error())
	}

	ms := uint64(t.UnixMilli())
	u[0] = byte(ms >> 40)
	u[1] = byte(ms >> 32)
	u[2] = byte(ms >> 24)
	u[3] = byte(ms >> 16)
	u[4] = byte(ms >> 8)
	u[5] = byte(ms)

	u[6] = (u[6] & 0x0F) | 0x70 // version 7
	u[8] = (u[8] & 0x3F) | 0x80 // variante RFC 4122

	return u
}

// Time extrait l'horodatage porté par un UUIDv7.
//
// Le résultat n'a de sens que pour un UUID de version 7 ; utiliser Version()
// pour s'en assurer si l'origine de l'identifiant n'est pas maîtrisée.
func (u UUID) Time() time.Time {
	ms := uint64(u[0])<<40 |
		uint64(u[1])<<32 |
		uint64(u[2])<<24 |
		uint64(u[3])<<16 |
		uint64(u[4])<<8 |
		uint64(u[5])
	return time.UnixMilli(int64(ms)).UTC()
}

// Version renvoie le numéro de version encodé dans l'UUID.
func (u UUID) Version() int { return int(u[6] >> 4) }

// IsZero indique si l'UUID est l'UUID nul.
func (u UUID) IsZero() bool { return u == Nil }

// String rend la forme canonique 8-4-4-4-12 en minuscules.
func (u UUID) String() string {
	var b [36]byte
	hex.Encode(b[0:8], u[0:4])
	b[8] = '-'
	hex.Encode(b[9:13], u[4:6])
	b[13] = '-'
	hex.Encode(b[14:18], u[6:8])
	b[18] = '-'
	hex.Encode(b[19:23], u[8:10])
	b[23] = '-'
	hex.Encode(b[24:36], u[10:16])
	return string(b[:])
}

// Parse lit un UUID canonique. Les formes sans tirets ou entre accolades sont
// refusées volontairement : une seule représentation acceptée, moins de
// surface d'ambiguïté quand la valeur vient d'une URL.
func Parse(s string) (UUID, error) {
	var u UUID
	if len(s) != 36 {
		return Nil, ErrInvalid
	}
	if s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return Nil, ErrInvalid
	}
	src := []byte(s[0:8] + s[9:13] + s[14:18] + s[19:23] + s[24:36])
	if _, err := hex.Decode(u[:], src); err != nil {
		return Nil, ErrInvalid
	}
	return u, nil
}

// MustParse est Parse, mais panique en cas d'erreur. Réservé aux constantes.
func MustParse(s string) UUID {
	u, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

// --- Interfaces d'encodage --------------------------------------------------

// MarshalText satisfait encoding.TextMarshaler (utilisé par encoding/json).
func (u UUID) MarshalText() ([]byte, error) { return []byte(u.String()), nil }

// UnmarshalText satisfait encoding.TextUnmarshaler.
func (u *UUID) UnmarshalText(b []byte) error {
	v, err := Parse(string(b))
	if err != nil {
		return err
	}
	*u = v
	return nil
}

// Value satisfait driver.Valuer : pgx encode la chaîne vers une colonne `uuid`.
func (u UUID) Value() (driver.Value, error) { return u.String(), nil }

// Scan satisfait sql.Scanner, pour lire une colonne `uuid` de PostgreSQL.
func (u *UUID) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*u = Nil
		return nil
	case string:
		return u.UnmarshalText([]byte(v))
	case []byte:
		if len(v) == 16 {
			copy(u[:], v)
			return nil
		}
		return u.UnmarshalText(v)
	case [16]byte:
		*u = UUID(v)
		return nil
	default:
		return fmt.Errorf("uid: type source non géré %T", src)
	}
}
