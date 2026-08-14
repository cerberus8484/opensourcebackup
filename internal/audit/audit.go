// Package audit records security-relevant and operationally important events.
//
// Design principles:
//   - Append-only: no UPDATE or DELETE in application code (enforced by RLS in production).
//   - Non-blocking: audit failures are logged but never block the main operation.
//   - Structured: action names follow the pattern resource.verb (e.g. policy.created).
//   - Honest: unknown/partial states are recorded as-is; no fabrication.
//
// Usage:
//
//	_ = h.auditStore.Append(ctx, audit.Event(audit.ActionPolicyCreated, audit.ResourcePolicy, id).
//	    By(audit.ActorAdmin).Severity(audit.SeverityInfo).WithDetails("name="+name).Build())
package audit

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ── Actor types ───────────────────────────────────────────────────────────────

type ActorType string

const (
	ActorAdmin     ActorType = "admin"     // human admin via dashboard
	ActorAgent     ActorType = "agent"     // backup agent
	ActorScheduler ActorType = "scheduler" // automated scheduler
	ActorSystem    ActorType = "system"    // internal system action
)

// ── Severity ──────────────────────────────────────────────────────────────────

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// ── Action constants — resource.verb format ───────────────────────────────────

type Action string

const (
	// Auth
	ActionAuthLogin     Action = "auth.login"
	ActionAuthLoginFail Action = "auth.login_fail"
	ActionAuthLogout    Action = "auth.logout"

	// System
	ActionSystemOperationStateChanged Action = "system.operation_state_changed"

	// Enrollment
	ActionEnrollmentTokenCreated Action = "enrollment_token.created"
	ActionAgentEnrolled          Action = "agent.enrolled"
	ActionTokenRevoked           Action = "token.revoked"

	// Repository
	ActionRepositoryCreated          Action = "repository.created"
	ActionRepositoryUpdated          Action = "repository.updated"
	ActionRepositoryDeleted          Action = "repository.deleted"
	ActionRepositoryImmutableChanged Action = "repository.immutable_mode_changed"

	// Policy
	ActionPolicyCreated Action = "policy.created"
	ActionPolicyUpdated Action = "policy.updated"
	ActionPolicyDeleted Action = "policy.deleted"

	// Backup
	ActionBackupStarted   Action = "backup.started"
	ActionBackupCompleted Action = "backup.completed"
	ActionBackupFailed    Action = "backup.failed"
	ActionBackupCancelled Action = "backup.cancelled"

	// Restore Test
	ActionRestoreTestCreated   Action = "restore_test.created"
	ActionRestoreTestCompleted Action = "restore_test.completed"
	ActionRestoreTestFailed    Action = "restore_test.failed"

	// Retention
	ActionRetentionJobCreated      Action = "retention.job_created"
	ActionRetentionCompleted       Action = "retention.completed"
	ActionRetentionFailed          Action = "retention.failed"
	ActionRetentionSnapshotRemoved Action = "retention.snapshot_removed"

	// GDPR
	ActionGDPRExport Action = "gdpr.export"
	ActionGDPRPurge  Action = "gdpr.purge"

	// Legacy aliases kept for backward compatibility
	ActionCreate                 = ActionRepositoryCreated
	ActionUpdate                 = ActionRepositoryUpdated
	ActionDelete                 = ActionRepositoryDeleted
	ActionPurge                  = ActionGDPRPurge
	ActionExport                 = ActionGDPRExport
	ActionLogin                  = ActionAuthLogin
	ActionLoginFail              = ActionAuthLoginFail
	ActionLogout                 = ActionAuthLogout
	ActionTokenRevoke            = ActionTokenRevoked
	ActionBackupStart            = ActionBackupStarted
	ActionBackupComplete         = ActionBackupCompleted
	ActionBackupFail             = ActionBackupFailed
	ActionRestoreStart    Action = "restore.started"
	ActionRestoreComplete Action = "restore.completed"
	ActionRestoreFail     Action = "restore.failed"
)

// ── Resource types ────────────────────────────────────────────────────────────

type ResourceType string

const (
	ResourceSystem      ResourceType = "system"
	ResourceRepository  ResourceType = "repository"
	ResourcePolicy      ResourceType = "policy"
	ResourceJob         ResourceType = "job"
	ResourceSnapshot    ResourceType = "snapshot"
	ResourceRestoreTest ResourceType = "restore_test"
	ResourceAuth        ResourceType = "auth"
	ResourceAgent       ResourceType = "agent"
	ResourceRetention   ResourceType = "retention"
)

// ── Entry ─────────────────────────────────────────────────────────────────────

// Entry is a single immutable audit record.
type Entry struct {
	ID           int64
	Timestamp    time.Time
	Action       Action
	ResourceType ResourceType
	ResourceID   string
	ActorType    ActorType // who triggered the event
	Actor        string    // specific actor: "admin", "agent:<system_id>"
	IP           string    // hashed client IP (security.ClientIPHashed)
	UserAgent    string
	Details      string // free-text; must NOT contain secrets or PII
	Severity     Severity
	Success      bool
}

// ── Builder ───────────────────────────────────────────────────────────────────

// builder provides a fluent API for constructing audit entries.
// Use audit.Event(...) to start a builder.
type builder struct {
	e Entry
}

// Event starts an audit entry builder with the mandatory fields.
func Event(action Action, resourceType ResourceType, resourceID string) *builder {
	return &builder{e: Entry{
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		ActorType:    ActorSystem,
		Severity:     SeverityInfo,
		Success:      true,
	}}
}

func (b *builder) By(t ActorType) *builder      { b.e.ActorType = t; return b }
func (b *builder) Actor(name string) *builder   { b.e.Actor = name; return b }
func (b *builder) IP(ip string) *builder        { b.e.IP = ip; return b }
func (b *builder) UA(ua string) *builder        { b.e.UserAgent = ua; return b }
func (b *builder) Details(d string) *builder    { b.e.Details = d; return b }
func (b *builder) Severity(s Severity) *builder { b.e.Severity = s; return b }
func (b *builder) Failed() *builder             { b.e.Success = false; return b }
func (b *builder) Build() Entry                 { return b.e }

// ── Store interface ───────────────────────────────────────────────────────────

// Store persists audit entries.
type Store interface {
	Append(ctx context.Context, e Entry) error
	List(ctx context.Context, resourceType ResourceType, resourceID string, limit int) ([]Entry, error)
}

// ── PostgresStore ─────────────────────────────────────────────────────────────

// PostgresStore implements Store against a PostgreSQL connection pool.
type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) Append(ctx context.Context, e Entry) error {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	if e.ActorType == "" {
		e.ActorType = ActorSystem
	}
	if e.Severity == "" {
		e.Severity = SeverityInfo
	}
	const q = `
		INSERT INTO audit_log
			(timestamp, action, resource_type, resource_id,
			 actor_type, actor, ip, user_agent, details, severity, success)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`
	_, err := s.pool.Exec(ctx, q,
		e.Timestamp, string(e.Action), string(e.ResourceType), e.ResourceID,
		string(e.ActorType), e.Actor, e.IP, e.UserAgent, e.Details,
		string(e.Severity), e.Success,
	)
	return err
}

func (s *PostgresStore) List(ctx context.Context, rt ResourceType, rid string, limit int) ([]Entry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	args := []any{limit}
	where := ""
	if rt != "" && rid != "" {
		where = "WHERE resource_type = $2 AND resource_id = $3"
		args = append(args, string(rt), rid)
	} else if rt != "" {
		where = "WHERE resource_type = $2"
		args = append(args, string(rt))
	}
	q := `SELECT id, timestamp, action, resource_type, resource_id,
	             actor_type, actor, ip, user_agent, details, severity, success
	      FROM audit_log ` + where + `
	      ORDER BY timestamp DESC LIMIT $1`

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var e Entry
		var resType, action, actorType, severity string
		if err := rows.Scan(
			&e.ID, &e.Timestamp, &action, &resType, &e.ResourceID,
			&actorType, &e.Actor, &e.IP, &e.UserAgent, &e.Details,
			&severity, &e.Success,
		); err != nil {
			return nil, err
		}
		e.Action = Action(action)
		e.ResourceType = ResourceType(resType)
		e.ActorType = ActorType(actorType)
		e.Severity = Severity(severity)
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// ── NoopStore ────────────────────────────────────────────────────────────────

type NoopStore struct{}

func (NoopStore) Append(_ context.Context, _ Entry) error { return nil }
func (NoopStore) List(_ context.Context, _ ResourceType, _ string, _ int) ([]Entry, error) {
	return nil, nil
}
