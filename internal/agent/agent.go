package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	agentclient "github.com/cerberus8484/opensourcebackup/internal/agent/client"
	"github.com/cerberus8484/opensourcebackup/internal/agent/outbox"
	"github.com/cerberus8484/opensourcebackup/internal/agent/restic"
	"github.com/cerberus8484/opensourcebackup/internal/catalog"
)

// ControlPlaneClient is the interface the agent uses to talk to the control plane.
// Defined here (DIP) so the agent package does not import the concrete HTTP client.
type ControlPlaneClient interface {
	// Heartbeat — called on every poll cycle to update last_seen_at on the control plane.
	// Returns ErrUnauthorized if the token was revoked (system deleted, re-enroll required).
	Heartbeat(ctx context.Context) error
	// Backup jobs
	ListPendingJobs(ctx context.Context) ([]catalog.BackupJob, error)
	GetPolicy(ctx context.Context, id uuid.UUID) (*catalog.BackupPolicy, error)
	StartJob(ctx context.Context, jobID uuid.UUID) error
	CompleteJob(ctx context.Context, jobID uuid.UUID, snapshotID string, bytesUploaded int64, paths []string) error
	FailJob(ctx context.Context, jobID uuid.UUID, reason string) error
	// ReportProgress sends a live progress snapshot while a backup runs (B_JOB_PROGRESS).
	ReportProgress(ctx context.Context, jobID uuid.UUID, p catalog.JobProgress) error
	// IsCancelRequested reports whether an operator requested a stop (B_JOB_CANCEL).
	IsCancelRequested(ctx context.Context, jobID uuid.UUID) (bool, string, error)
	// CancelledJob reports that the agent stopped a job after a cancel request.
	CancelledJob(ctx context.Context, jobID uuid.UUID, reason string) error
	// Repository lookup (agent reads location from policy)
	GetRepository(ctx context.Context, id uuid.UUID) (*catalog.BackupRepository, error)
	// Restore tests
	ClaimNextRestoreTest(ctx context.Context) (*catalog.RestoreTest, error)
	GetSnapshot(ctx context.Context, id uuid.UUID) (*catalog.Snapshot, error)
	CompleteRestoreTest(ctx context.Context, id uuid.UUID, files int, bytes int64) error
	FailRestoreTest(ctx context.Context, id uuid.UUID, reason string) error
}

// Config holds all runtime parameters for the agent.
type Config struct {
	PollInterval    time.Duration
	ResticBin       string
	ResticPassword  string
	ResticRepo      string
	RestoreTestRoot string // sandbox root for restore tests
	OutboxDir       string // persistent store for undelivered terminal events (default: data/outbox)
}

// Agent polls the control plane for pending jobs and executes them.
type Agent struct {
	cfg    Config
	cp     ControlPlaneClient
	runner *restic.Runner
	log    *slog.Logger
	outbox *outbox.Store // durable at-least-once delivery of terminal outcomes (nil = best-effort)
}

// New creates an Agent.
func New(cfg Config, cp ControlPlaneClient, log *slog.Logger) *Agent {
	a := &Agent{
		cfg:    cfg,
		cp:     cp,
		runner: restic.New(cfg.ResticBin),
		log:    log,
	}
	dir := cfg.OutboxDir
	if dir == "" {
		dir = filepath.Join("data", "outbox")
	}
	if ob, err := outbox.NewStore(dir); err != nil {
		// Non-fatal: without the outbox, terminal reports fall back to best-effort
		// direct delivery. Log loudly — this weakens the delivery guarantee.
		log.Error("could not open outbox — terminal reports will be best-effort", "dir", dir, "error", err)
	} else {
		a.outbox = ob
	}
	return a
}

// Run starts the poll loop and blocks until ctx is canceled or a fatal error occurs.
// Returns a non-nil error if the agent token is rejected (re-enrollment required).
func (a *Agent) Run(ctx context.Context) error {
	a.log.Info("agent started", "poll_interval", a.cfg.PollInterval)
	a.log.Info("restic process tuning applied", "mode", restic.ProcessTuningMode())

	// Deliver any terminal outcomes left over by a crash or a control-plane outage,
	// and keep retrying undelivered ones for as long as the agent runs.
	go a.drainOutbox(ctx)

	ticker := time.NewTicker(a.cfg.PollInterval)
	defer ticker.Stop()

	// Poll once immediately, then on each tick.
	if err := a.poll(ctx); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			a.log.Info("agent stopped")
			return nil
		case <-ticker.C:
			if err := a.poll(ctx); err != nil {
				return err
			}
		}
	}
}

func (a *Agent) poll(ctx context.Context) error {
	// Heartbeat first — updates last_seen on the control plane.
	// On 401 the token was revoked; stop immediately.
	if err := a.cp.Heartbeat(ctx); err != nil {
		if isUnauthorized(err) {
			a.log.Error("heartbeat rejected — token revoked, re-enrollment required")
			return err
		}
		// Network errors are non-fatal — agent continues working even if
		// the control plane is temporarily unreachable.
		a.log.Warn("heartbeat failed", "error", err)
	}

	// Poll backup jobs
	jobs, err := a.cp.ListPendingJobs(ctx)
	if err != nil {
		if isUnauthorized(err) {
			a.log.Error("agent token rejected — re-enrollment required")
			return err
		}
		a.log.Warn("poll jobs failed", "error", err)
		return nil
	}
	for _, job := range jobs {
		switch job.Type {
		case catalog.JobTypeRetention:
			// Retention jobs are handled by the retention pipeline
		case "verify":
			a.executeVerifyJob(ctx, job)
		default:
			a.executeJob(ctx, job)
		}
	}

	// Poll restore tests
	if err := a.executeNextRestoreTest(ctx); err != nil && isUnauthorized(err) {
		return err
	}
	return nil
}

// executeVerifyJob runs restic check for a verify job.
func (a *Agent) executeVerifyJob(ctx context.Context, job catalog.BackupJob) {
	log := a.log.With("job_id", job.ID, "policy_id", job.PolicyID)
	log.Info("verify job started")

	policy, err := a.cp.GetPolicy(ctx, job.PolicyID)
	if err != nil {
		log.Error("get policy failed", "error", err)
		a.doFail(ctx, log, job.ID, fmt.Sprintf("get policy: %s", err))
		return
	}

	repo, err := a.cp.GetRepository(ctx, *policy.RepositoryID)
	if err != nil {
		log.Error("get repository failed", "error", err)
		a.doFail(ctx, log, job.ID, fmt.Sprintf("get repository: %s", err))
		return
	}

	if err := a.cp.StartJob(ctx, job.ID); err != nil {
		if errors.Is(err, agentclient.ErrDispatchDeferred) {
			log.Info("dispatch deferred — agent at concurrency limit; verify job stays pending")
			return
		}
		log.Error("start job failed", "error", err)
		return
	}

	err = a.runner.Verify(ctx, restic.VerifyOptions{
		Repo:     repo.Location,
		Password: a.cfg.ResticPassword,
		ReadData: false, // fast check by default
	})
	if err != nil {
		log.Error("verify failed", "error", err)
		a.doFail(ctx, log, job.ID, err.Error())
		return
	}

	// Complete with zero bytes (verify doesn't transfer data)
	a.reportTerminal(ctx, log, outbox.Event{JobID: job.ID, Kind: outbox.KindComplete})
	log.Info("verify job completed")
}

func (a *Agent) executeNextRestoreTest(ctx context.Context) error {
	rt, err := a.cp.ClaimNextRestoreTest(ctx)
	if err != nil {
		if isUnauthorized(err) {
			return err
		}
		// ErrNotFound = no pending restore test — normal
		return nil
	}

	log := a.log.With("restore_test_id", rt.ID, "snapshot_id", rt.SnapshotID)
	log.Info("restore test started")

	root := a.cfg.RestoreTestRoot
	if root == "" {
		root = filepath.Join("data", "restore-tests")
	}
	target := filepath.Join(root, rt.ID.String())
	if rt.TargetPath != nil && *rt.TargetPath != "" {
		target = *rt.TargetPath
	}

	snap, err := a.cp.GetSnapshot(ctx, rt.SnapshotID)
	if err != nil {
		log.Error("could not load snapshot for restore test", "error", err)
		a.cp.FailRestoreTest(ctx, rt.ID, fmt.Sprintf("snapshot lookup: %s", err)) //nolint:errcheck
		return nil
	}

	// Use the repository that the snapshot actually belongs to, not the
	// agent's default configured repo — they may differ.
	repo, err := a.cp.GetRepository(ctx, rt.RepositoryID)
	if err != nil {
		log.Error("could not load repository for restore test", "error", err)
		a.cp.FailRestoreTest(ctx, rt.ID, fmt.Sprintf("repository lookup: %s", err)) //nolint:errcheck
		return nil
	}

	// If the user explicitly set a custom target path, skip the RestoreRoot
	// sandbox constraint — the user knows where they want to restore.
	restoreRoot := root
	if rt.TargetPath != nil && *rt.TargetPath != "" {
		restoreRoot = ""
	}

	result, err := a.runner.Restore(ctx, restic.RestoreOptions{
		Repo:        repo.Location,
		Password:    a.cfg.ResticPassword,
		SnapshotID:  snap.EngineSnapshotID,
		TargetPath:  target,
		RestoreRoot: restoreRoot,
	})
	if err != nil {
		log.Error("restore test failed", "error", err)
		if failErr := a.cp.FailRestoreTest(ctx, rt.ID, err.Error()); failErr != nil {
			log.Error("fail restore test update failed", "error", failErr)
		}
		return nil
	}

	if err := a.cp.CompleteRestoreTest(ctx, rt.ID, result.VerifiedFiles, result.VerifiedBytes); err != nil {
		log.Error("complete restore test update failed", "error", err)
		return nil
	}
	log.Info("restore test completed",
		"verified_files", result.VerifiedFiles,
		"verified_bytes", result.VerifiedBytes,
		"target", result.TargetPath,
	)
	return nil
}

func (a *Agent) executeJob(ctx context.Context, job catalog.BackupJob) {
	log := a.log.With("job_id", job.ID, "policy_id", job.PolicyID)

	policy, err := a.cp.GetPolicy(ctx, job.PolicyID)
	if err != nil {
		log.Error("get policy failed", "error", err)
		a.doFail(ctx, log, job.ID, fmt.Sprintf("get policy: %s", err))
		return
	}

	if policy.RepositoryID == nil {
		a.doFail(ctx, log, job.ID, "policy has no repository configured")
		log.Error("policy has no repository_id — cannot determine backup target")
		return
	}

	// Fetch the repository to get the actual backup destination (Location).
	// This allows each policy to target a different repo — NAS, S3, local path, etc.
	repo, err := a.cp.GetRepository(ctx, *policy.RepositoryID)
	if err != nil {
		log.Error("could not load repository", "error", err)
		a.doFail(ctx, log, job.ID, fmt.Sprintf("repository lookup: %s", err))
		return
	}
	// Use policy repository location; fall back to agent env var if empty.
	repoLocation := repo.Location
	if repoLocation == "" {
		repoLocation = a.cfg.ResticRepo
	}

	if err := a.cp.StartJob(ctx, job.ID); err != nil {
		if errors.Is(err, agentclient.ErrDispatchDeferred) {
			// Not a failure: the control plane declined to start now (e.g. agent
			// concurrency limit). Leave the job pending and retry on the next poll.
			log.Info("dispatch deferred — agent at concurrency limit; job stays pending")
			return
		}
		log.Error("mark job running failed", "error", err)
		return
	}
	log.Info("job started",
		"engine", policy.Engine,
		"includes", policy.Includes,
		"repository", repoLocation,
	)

	// Live progress reporting (B_JOB_PROGRESS): throttle to ~2s and compute
	// throughput from the byte delta. Best-effort — never fails the backup.
	var (
		lastReport time.Time
		lastBytes  int64
	)
	onProgress := func(p restic.Progress) {
		now := time.Now()
		if !lastReport.IsZero() && now.Sub(lastReport) < 2*time.Second {
			return
		}
		var bps int64
		if !lastReport.IsZero() {
			if dt := now.Sub(lastReport).Seconds(); dt > 0 && p.BytesDone >= lastBytes {
				bps = int64(float64(p.BytesDone-lastBytes) / dt)
			}
		}
		lastReport = now
		lastBytes = p.BytesDone
		if err := a.cp.ReportProgress(ctx, job.ID, catalog.JobProgress{
			Phase:         p.Phase,
			Percent:       p.Percent,
			BytesDone:     p.BytesDone,
			BytesTotal:    p.TotalBytes,
			FilesDone:     p.FilesDone,
			FilesTotal:    p.TotalFiles,
			ThroughputBps: bps,
		}); err != nil {
			log.Debug("report progress failed (best-effort)", "error", err)
		}
	}

	// Cooperative cancellation (B_JOB_CANCEL): a watcher goroutine polls the control
	// plane while the backup runs; on an operator stop it cancels jobCtx, which kills
	// restic. The deliberate stop is reported as "cancelled", not "failed".
	jobCtx, cancelJob := context.WithCancel(ctx)
	defer cancelJob()
	var cancelled atomic.Bool
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-jobCtx.Done():
				return
			case <-t.C:
				requested, _, err := a.cp.IsCancelRequested(ctx, job.ID)
				if err != nil {
					continue // best-effort; control plane may be briefly unreachable
				}
				if requested {
					cancelled.Store(true)
					log.Info("cancel requested by operator — stopping backup")
					cancelJob()
					return
				}
			}
		}
	}()

	result, err := a.runner.Backup(jobCtx, restic.BackupOptions{
		Repo:       repoLocation,
		Password:   a.cfg.ResticPassword,
		Includes:   policy.Includes,
		Excludes:   policy.Excludes,
		Tags:       []string{fmt.Sprintf("policy=%s", policy.ID)},
		OnProgress: onProgress,
	})
	if err != nil {
		// Report on the parent ctx — jobCtx is already cancelled.
		if cancelled.Load() {
			log.Info("backup cancelled by operator")
			a.reportTerminal(ctx, log, outbox.Event{
				JobID: job.ID, Kind: outbox.KindCancelled, Reason: "stopped by operator",
			})
			return
		}
		log.Error("backup failed", "error", err)
		a.doFail(ctx, log, job.ID, err.Error())
		return
	}

	a.reportTerminal(ctx, log, outbox.Event{
		JobID:      job.ID,
		Kind:       outbox.KindComplete,
		SnapshotID: result.SnapshotID,
		Bytes:      result.BytesAdded,
		Paths:      policy.Includes,
	})

	log.Info("job completed",
		"snapshot_id", result.SnapshotID,
		"bytes_added", result.BytesAdded,
		"files_new", result.FilesNew,
		"files_changed", result.FilesChanged,
	)
}

func (a *Agent) doFail(ctx context.Context, log *slog.Logger, jobID uuid.UUID, reason string) {
	a.reportTerminal(ctx, log, outbox.Event{JobID: jobID, Kind: outbox.KindFail, Reason: reason})
}

// reportTerminal durably records a job's terminal outcome, then attempts immediate
// delivery. On a transient failure the event stays in the outbox and drainOutbox
// keeps retrying until the control plane confirms it — so a brief control-plane
// outage or an agent restart can never lose a real outcome.
func (a *Agent) reportTerminal(ctx context.Context, log *slog.Logger, e outbox.Event) {
	if a.outbox == nil { // outbox unavailable — best-effort direct delivery
		if err := a.deliver(ctx, e); err != nil {
			log.Error("terminal report failed (no outbox)", "kind", e.Kind, "error", err)
		}
		return
	}
	if err := a.outbox.Enqueue(e); err != nil {
		log.Error("could not persist terminal event — trying direct delivery", "error", err)
		if derr := a.deliver(ctx, e); derr != nil {
			log.Error("terminal report failed", "kind", e.Kind, "error", derr)
		}
		return
	}
	a.tryDeliver(ctx, log, e)
}

// tryDeliver attempts one delivery and acks the event on success or on a permanent
// rejection (the control plane already has a terminal state for this job). A
// transient failure is left in the outbox for the next drain.
func (a *Agent) tryDeliver(ctx context.Context, log *slog.Logger, e outbox.Event) {
	err := a.deliver(ctx, e)
	switch {
	case err == nil:
		_ = a.outbox.Ack(e.JobID)
	case isPermanent(err):
		log.Warn("control plane rejected terminal report — dropping",
			"kind", e.Kind, "job_id", e.JobID, "error", err)
		_ = a.outbox.Ack(e.JobID)
	default:
		log.Warn("terminal report not delivered yet — will retry",
			"kind", e.Kind, "job_id", e.JobID, "error", err)
	}
}

// deliver sends one terminal event to the control plane via the matching endpoint.
func (a *Agent) deliver(ctx context.Context, e outbox.Event) error {
	switch e.Kind {
	case outbox.KindComplete:
		return a.cp.CompleteJob(ctx, e.JobID, e.SnapshotID, e.Bytes, e.Paths)
	case outbox.KindFail:
		return a.cp.FailJob(ctx, e.JobID, e.Reason)
	case outbox.KindCancelled:
		return a.cp.CancelledJob(ctx, e.JobID, e.Reason)
	default:
		return fmt.Errorf("unknown outbox event kind %q", e.Kind)
	}
}

// drainOutbox retries delivery of persisted terminal events until the control plane
// confirms them. Runs once at startup (delivering anything a crash or outage left
// behind) and then on a ticker.
func (a *Agent) drainOutbox(ctx context.Context) {
	if a.outbox == nil {
		return
	}
	const interval = 30 * time.Second
	drain := func() {
		events, err := a.outbox.List()
		if err != nil {
			a.log.Error("outbox list failed", "error", err)
			return
		}
		for _, e := range events {
			select {
			case <-ctx.Done():
				return
			default:
			}
			a.tryDeliver(ctx, a.log.With("job_id", e.JobID), e)
		}
	}
	drain()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			drain()
		}
	}
}

// isPermanent reports whether a delivery error means retrying can never succeed:
// the control plane already holds a terminal state for the job (conflict), the job
// is gone (not found), or the token was revoked. Everything else (network, 5xx) is
// transient and worth retrying.
func isPermanent(err error) bool {
	return errors.Is(err, agentclient.ErrConflict) ||
		errors.Is(err, agentclient.ErrNotFound) ||
		errors.Is(err, agentclient.ErrUnauthorized)
}

func isUnauthorized(err error) bool {
	return errors.Is(err, agentclient.ErrUnauthorized)
}
