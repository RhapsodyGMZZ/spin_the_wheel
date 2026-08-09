package store

import (
	"context"
	"encoding/json"

	"spinwheel/internal/uid"
)

// Actions journalisées. Une constante par événement pour éviter les fautes de
// frappe dans les requêtes d'analyse.
const (
	ActionLoginStarted   = "auth.login_started"
	ActionLoginSucceeded = "auth.login_succeeded"
	ActionLoginDenied    = "auth.login_denied"
	ActionLoginFailed    = "auth.login_failed"
	ActionLogout         = "auth.logout"

	ActionWheelCreated  = "wheel.created"
	ActionWheelUpdated  = "wheel.updated"
	ActionWheelDeleted  = "wheel.deleted"
	ActionSegmentsSaved = "wheel.segments_saved"

	ActionImageUploaded = "image.uploaded"

	ActionSpin = "wheel.spin"

	ActionAllowedEmailAdded   = "allowlist.added"
	ActionAllowedEmailRemoved = "allowlist.removed"

	ActionRateLimited = "security.rate_limited"
	ActionCSRFBlocked = "security.csrf_blocked"
	ActionForbidden   = "security.forbidden"
)

// AuditEntry est une ligne du journal d'audit.
type AuditEntry struct {
	ActorID    uid.UUID
	Action     string
	EntityType string
	EntityID   uid.UUID
	IP         string
	UserAgent  string
	RequestID  string
	Details    map[string]any
}

// Audit écrit une ligne dans le journal d'audit.
//
// Les erreurs sont renvoyées à l'appelant mais ne doivent jamais faire échouer
// l'action métier : un journal indisponible ne justifie pas de refuser un
// tirage. Les appelants se contentent de les logguer.
func (s *Store) Audit(ctx context.Context, e AuditEntry) error {
	details := []byte("{}")
	if len(e.Details) > 0 {
		if b, err := json.Marshal(e.Details); err == nil {
			details = b
		}
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO audit_log (id, actor_id, action, entity_type, entity_id, ip, user_agent, request_id, details)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::jsonb)`,
		uid.New(),
		nullUUID(e.ActorID),
		e.Action,
		e.EntityType,
		nullUUID(e.EntityID),
		e.IP,
		truncate(e.UserAgent, 512),
		e.RequestID,
		string(details),
	)
	return err
}
