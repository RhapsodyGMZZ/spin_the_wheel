// Package logging fournit le journal structuré du service.
//
// Tout part sur la sortie standard au format JSON : Docker le capte, nginx n'a
// rien à faire, et `docker compose logs` reste grep-able. Une ligne = un
// événement, avec toujours un request_id pour recoller une requête HTTP à ses
// effets en base.
package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// Clés de contexte, non exportées pour éviter toute collision.
type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota
	ctxKeyLogger
)

// New construit le logger racine.
func New(level string) *slog.Logger {
	var lv slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		lv = slog.LevelDebug
	case "warn", "warning":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}

	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: lv,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Horodatage en UTC, format RFC3339 avec millisecondes.
			if a.Key == slog.TimeKey && len(groups) == 0 {
				a.Value = slog.StringValue(a.Value.Time().UTC().Format("2006-01-02T15:04:05.000Z"))
			}
			return a
		},
	})
	return slog.New(h)
}

// WithRequestID attache un identifiant de requête au contexte.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyRequestID, id)
}

// RequestID relit l'identifiant de requête, ou "" s'il n'y en a pas.
func RequestID(ctx context.Context) string {
	if v, ok := ctx.Value(ctxKeyRequestID).(string); ok {
		return v
	}
	return ""
}

// WithLogger attache un logger enrichi au contexte.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKeyLogger, l)
}

// From récupère le logger du contexte, ou le logger par défaut.
func From(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(ctxKeyLogger).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}
