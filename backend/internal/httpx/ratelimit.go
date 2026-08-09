package httpx

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Limiter est un seau à jetons en mémoire, indexé par clé (généralement une
// adresse IP).
//
// Le stockage est volontairement local au processus : le service tourne en un
// seul conteneur derrière nginx. Passer à plusieurs répliques demanderait de
// déplacer l'état ailleurs (Redis, ou limit_req côté nginx).
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket

	rate  float64 // jetons régénérés par seconde
	burst float64 // capacité du seau
	idle  time.Duration
	max   int // nombre maximal de seaux vivants
}

// maxBuckets borne la table des seaux.
//
// Une clé de limitation dérive toujours d'une donnée venant de la requête. Si
// cette donnée a un espace de valeurs large, un appelant peut créer un seau
// neuf à chaque requête : le quota ne mord jamais et la mémoire enfle jusqu'au
// prochain passage du concierge. Le plafond transforme ce scénario en un refus
// franc plutôt qu'en une fuite de mémoire.
const maxBuckets = 50_000

type bucket struct {
	tokens float64
	last   time.Time
}

// NewLimiter construit un limiteur autorisant `perMinute` requêtes par minute,
// avec une réserve de `burst` requêtes instantanées.
func NewLimiter(perMinute, burst int) *Limiter {
	if perMinute < 1 {
		perMinute = 1
	}
	if burst < 1 {
		burst = 1
	}
	return &Limiter{
		buckets: make(map[string]*bucket),
		rate:    float64(perMinute) / 60.0,
		burst:   float64(burst),
		idle:    10 * time.Minute,
		max:     maxBuckets,
	}
}

// Allow consomme un jeton pour la clé donnée. Rend false si le seau est vide,
// avec le délai à attendre avant la prochaine tentative.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= l.max {
			l.purgeLocked(now.Add(-l.idle))
			if len(l.buckets) >= l.max {
				// Table saturée : refuser est le seul comportement sûr.
				return false, time.Minute
			}
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}

	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens = math.Min(l.burst, b.tokens+elapsed*l.rate)
		b.last = now
	}

	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
	return false, wait
}

// StartJanitor purge périodiquement les seaux inactifs pour que la table ne
// grossisse pas indéfiniment sous un flux d'IP variées.
func (l *Limiter) StartJanitor(ctx context.Context) {
	go func() {
		t := time.NewTicker(l.idle)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				l.mu.Lock()
				l.purgeLocked(time.Now().Add(-l.idle))
				l.mu.Unlock()
			}
		}
	}()
}

// purgeLocked supprime les seaux inactifs depuis `cutoff`.
// L'appelant doit détenir le verrou.
func (l *Limiter) purgeLocked(cutoff time.Time) {
	for k, b := range l.buckets {
		if b.last.Before(cutoff) {
			delete(l.buckets, k)
		}
	}
}

// Size rend le nombre de seaux vivants. Utile aux tests et à la métrologie.
func (l *Limiter) Size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// RateLimitFunc calcule la clé de limitation d'une requête.
type RateLimitFunc func(r *http.Request) string

// RateLimit refuse les requêtes au-delà du quota, avec un en-tête Retry-After.
// onBlock, s'il est fourni, est appelé pour journaliser le refus.
func RateLimit(l *Limiter, key RateLimitFunc, onBlock func(r *http.Request, key string)) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			k := key(r)
			ok, wait := l.Allow(k)
			if !ok {
				if onBlock != nil {
					onBlock(r, k)
				}
				secs := int(wait.Seconds())
				if secs < 1 {
					secs = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(secs))
				Err(w, r, http.StatusTooManyRequests, CodeRateLimited,
					"Trop de requêtes. Réessayez dans quelques instants.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
