package client

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/cerberus8484/opensourcebackup/internal/catalog"
)

const defaultTimeout = 30 * time.Second

// ErrUnauthorized is returned when the control plane rejects the agent token.
// The agent should stop and be re-enrolled.
var ErrUnauthorized = errors.New("agent: unauthorized — token revoked or invalid")

// ErrConflict is returned on HTTP 409 — the job is no longer in a state that
// allows the requested change (e.g. a terminal outcome reported for a job the
// reaper already failed). The outbox treats this as permanent: retrying a
// conflicting terminal report never succeeds, so the event is dropped, not looped.
var ErrConflict = errors.New("agent: conflict — job no longer accepts this change")

// ErrNotFound is returned on HTTP 404 — the job/resource no longer exists.
// Also permanent from the outbox's perspective.
var ErrNotFound = errors.New("agent: not found")

// Client communicates with the OpensourceBackup control plane using
// the authenticated /v1/agent/* routes.
type Client struct {
	baseURL    string
	token      string // Bearer token — never log this
	httpClient *http.Client
}

// New creates a Client pointing at baseURL with the given bearer token.
// If skipTLSVerify is true, self-signed certificates are accepted (dev only).
func New(baseURL, token string, skipTLSVerify ...bool) *Client {
	transport := http.DefaultTransport
	if len(skipTLSVerify) > 0 && skipTLSVerify[0] {
		transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // dev-only, documented
		}
	}
	return &Client{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout:   defaultTimeout,
			Transport: transport,
		},
	}
}

// HeartbeatResponse is the server response to a heartbeat.
type HeartbeatResponse struct {
	Status            string `json:"status"`
	ServerTime        string `json:"server_time"`
	RecommendedVersion string `json:"recommended_version"`
	UpdateAvailable   bool   `json:"update_available"`
	UpdateDownloadURL string `json:"update_download_url"`
}

// Heartbeat stamps last_seen on the control plane for the authenticated system.
// Returns the server response (including update info) and ErrUnauthorized if token revoked.
func (c *Client) Heartbeat(ctx context.Context) error {
	var resp HeartbeatResponse
	if err := c.put(ctx, c.baseURL+"/v1/agent/heartbeat", nil); err != nil {
		return fmt.Errorf("heartbeat: %w", err)
	}
	_ = resp // future: check update_available and log
	return nil
}

// ListPendingJobs returns pending jobs for the authenticated system.
// The system is identified by the bearer token — no system_id parameter needed.
func (c *Client) ListPendingJobs(ctx context.Context) ([]catalog.BackupJob, error) {
	var jobs []catalog.BackupJob
	if err := c.get(ctx, c.baseURL+"/v1/agent/jobs", &jobs); err != nil {
		return nil, fmt.Errorf("list pending jobs: %w", err)
	}
	return jobs, nil
}

// GetPolicy returns the policy with the given ID.
func (c *Client) GetPolicy(ctx context.Context, id uuid.UUID) (*catalog.BackupPolicy, error) {
	url := fmt.Sprintf("%s/v1/policies/%s", c.baseURL, id)
	var p catalog.BackupPolicy
	if err := c.get(ctx, url, &p); err != nil {
		return nil, fmt.Errorf("get policy %s: %w", id, err)
	}
	return &p, nil
}

// StartJob marks a job as running.
func (c *Client) StartJob(ctx context.Context, jobID uuid.UUID) error {
	url := fmt.Sprintf("%s/v1/agent/jobs/%s/start", c.baseURL, jobID)
	if err := c.put(ctx, url, nil); err != nil {
		return fmt.Errorf("start job %s: %w", jobID, err)
	}
	return nil
}

// CompleteJob marks a job as successful and registers the resulting snapshot.
func (c *Client) CompleteJob(ctx context.Context, jobID uuid.UUID, snapshotID string, bytesUploaded int64, paths []string) error {
	url := fmt.Sprintf("%s/v1/agent/jobs/%s/complete", c.baseURL, jobID)
	body := map[string]any{
		"engine_snapshot_id": snapshotID,
		"bytes_uploaded":     bytesUploaded,
		"paths":              paths,
	}
	if err := c.put(ctx, url, body); err != nil {
		return fmt.Errorf("complete job %s: %w", jobID, err)
	}
	return nil
}

// FailJob marks a job as failed with an error summary.
func (c *Client) FailJob(ctx context.Context, jobID uuid.UUID, reason string) error {
	url := fmt.Sprintf("%s/v1/agent/jobs/%s/fail", c.baseURL, jobID)
	body := map[string]any{"error_summary": reason}
	if err := c.put(ctx, url, body); err != nil {
		return fmt.Errorf("fail job %s: %w", jobID, err)
	}
	return nil
}

// ReportProgress sends a live progress snapshot for a running job (B_JOB_PROGRESS).
// Best-effort: the caller must not fail the backup if this errors.
func (c *Client) ReportProgress(ctx context.Context, jobID uuid.UUID, p catalog.JobProgress) error {
	url := fmt.Sprintf("%s/v1/agent/jobs/%s/progress", c.baseURL, jobID)
	if err := c.put(ctx, url, p); err != nil {
		return fmt.Errorf("report progress %s: %w", jobID, err)
	}
	return nil
}

// IsCancelRequested asks the control plane whether an operator requested a stop of
// this running job (B_JOB_CANCEL). Returns (requested, reason, error).
func (c *Client) IsCancelRequested(ctx context.Context, jobID uuid.UUID) (bool, string, error) {
	url := fmt.Sprintf("%s/v1/agent/jobs/%s/cancel-requested", c.baseURL, jobID)
	var resp struct {
		CancelRequested bool   `json:"cancel_requested"`
		Reason          string `json:"reason"`
	}
	if err := c.get(ctx, url, &resp); err != nil {
		return false, "", fmt.Errorf("cancel status %s: %w", jobID, err)
	}
	return resp.CancelRequested, resp.Reason, nil
}

// CancelledJob reports that the agent stopped a job in response to a cancel request.
func (c *Client) CancelledJob(ctx context.Context, jobID uuid.UUID, reason string) error {
	url := fmt.Sprintf("%s/v1/agent/jobs/%s/cancelled", c.baseURL, jobID)
	body := map[string]any{"reason": reason}
	if err := c.put(ctx, url, body); err != nil {
		return fmt.Errorf("cancelled job %s: %w", jobID, err)
	}
	return nil
}

// GetRepository returns the repository with the given ID.
func (c *Client) GetRepository(ctx context.Context, id uuid.UUID) (*catalog.BackupRepository, error) {
	url := fmt.Sprintf("%s/v1/repositories/%s", c.baseURL, id)
	var r catalog.BackupRepository
	if err := c.get(ctx, url, &r); err != nil {
		return nil, fmt.Errorf("get repository %s: %w", id, err)
	}
	return &r, nil
}

// ClaimNextRestoreTest claims the next pending restore test for this system.
func (c *Client) ClaimNextRestoreTest(ctx context.Context) (*catalog.RestoreTest, error) {
	var tests []catalog.RestoreTest
	if err := c.get(ctx, c.baseURL+"/v1/agent/restore-tests", &tests); err != nil {
		return nil, fmt.Errorf("claim restore test: %w", err)
	}
	if len(tests) == 0 {
		return nil, catalog.ErrNotFound
	}
	return &tests[0], nil
}

// GetSnapshot returns the snapshot with the given ID.
func (c *Client) GetSnapshot(ctx context.Context, id uuid.UUID) (*catalog.Snapshot, error) {
	url := fmt.Sprintf("%s/v1/agent/snapshots/%s", c.baseURL, id)
	var s catalog.Snapshot
	if err := c.get(ctx, url, &s); err != nil {
		return nil, fmt.Errorf("get snapshot %s: %w", id, err)
	}
	return &s, nil
}

// CompleteRestoreTest marks a restore test as successful.
func (c *Client) CompleteRestoreTest(ctx context.Context, id uuid.UUID, files int, bytes int64) error {
	url := fmt.Sprintf("%s/v1/agent/restore-tests/%s/complete", c.baseURL, id)
	body := map[string]any{"verified_files": files, "verified_bytes": bytes}
	if err := c.put(ctx, url, body); err != nil {
		return fmt.Errorf("complete restore test %s: %w", id, err)
	}
	return nil
}

// FailRestoreTest marks a restore test as failed.
func (c *Client) FailRestoreTest(ctx context.Context, id uuid.UUID, reason string) error {
	url := fmt.Sprintf("%s/v1/agent/restore-tests/%s/fail", c.baseURL, id)
	body := map[string]any{"error_summary": reason}
	if err := c.put(ctx, url, body); err != nil {
		return fmt.Errorf("fail restore test %s: %w", id, err)
	}
	return nil
}

// Enroll exchanges a one-time enrollment token for a long-lived agent token.
func (c *Client) Enroll(ctx context.Context, enrollmentToken string) (string, error) {
	body := map[string]string{"enrollment_token": enrollmentToken}
	var resp struct {
		Token string `json:"token"`
	}
	if err := c.post(ctx, c.baseURL+"/v1/agent/enroll", body, &resp); err != nil {
		return "", fmt.Errorf("enroll: %w", err)
	}
	return resp.Token, nil
}

// ── internal helpers ─────────────────────────────────────────────────────────

func (c *Client) get(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	c.setHeaders(req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck // HTTP response body close errors are not actionable
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode == http.StatusConflict {
		return ErrConflict
	}
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) put(ctx context.Context, url string, body any) error {
	return c.sendJSON(ctx, http.MethodPut, url, body, nil)
}

func (c *Client) post(ctx context.Context, url string, body any, out any) error {
	return c.sendJSON(ctx, http.MethodPost, url, body, out)
}

func (c *Client) sendJSON(ctx context.Context, method, url string, body any, out any) error {
	var b []byte
	var err error
	if body != nil {
		b, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	c.setHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode == http.StatusUnauthorized {
		return ErrUnauthorized
	}
	if resp.StatusCode == http.StatusConflict {
		return ErrConflict
	}
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *Client) setHeaders(req *http.Request) {
	if c.token != "" {
		// Never log the Authorization header
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
}
