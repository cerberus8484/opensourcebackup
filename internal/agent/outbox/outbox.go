// Package outbox gives the agent an at-least-once delivery guarantee for terminal
// job outcomes. A backup that finished (succeeded / failed / cancelled) MUST reach
// the control plane even if the control plane is briefly unreachable or the agent
// restarts mid-report — otherwise the dashboard keeps showing "running" forever and
// the whole UI becomes untrustworthy.
//
// The outbox persists each terminal event to disk BEFORE attempting delivery, then
// a drain loop retries delivery until the control plane confirms it (2xx) or
// permanently rejects it (the job is already terminal there). This is the
// server-side guarded state machine's local counterpart: together they guarantee a
// job's true outcome is recorded exactly once and never lost.
package outbox

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Kind identifies which terminal report an event carries.
type Kind string

const (
	KindComplete  Kind = "complete"
	KindFail      Kind = "fail"
	KindCancelled Kind = "cancelled"
)

// Event is a durable record of a job's terminal outcome awaiting delivery.
type Event struct {
	JobID      uuid.UUID `json:"job_id"`
	Kind       Kind      `json:"kind"`
	SnapshotID string    `json:"snapshot_id,omitempty"`
	Bytes      int64     `json:"bytes,omitempty"`
	Paths      []string  `json:"paths,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// Store persists terminal events on disk, one JSON file per job — a job has exactly
// one terminal outcome, so the job ID is a natural, idempotent key. Safe for
// concurrent use.
type Store struct {
	dir string
	mu  sync.Mutex
}

// NewStore opens (creating if needed) an outbox rooted at dir.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create outbox dir: %w", err)
	}
	return &Store{dir: dir}, nil
}

func (s *Store) path(jobID uuid.UUID) string {
	return filepath.Join(s.dir, jobID.String()+".json")
}

// Enqueue durably records a terminal event. Written atomically (temp file + rename)
// so a crash mid-write never leaves a half-written event on disk. Re-enqueuing the
// same job overwrites — the latest known outcome wins.
func (s *Store) Enqueue(e Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	final := s.path(e.JobID)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write outbox event: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("commit outbox event: %w", err)
	}
	return nil
}

// List returns all pending events, oldest first. Corrupt or unreadable files are
// skipped rather than failing the whole drain.
func (s *Store) List() ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read outbox dir: %w", err)
	}
	var events []Event
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, name))
		if err != nil {
			continue
		}
		var e Event
		if err := json.Unmarshal(data, &e); err != nil {
			continue
		}
		events = append(events, e)
	}
	sort.Slice(events, func(i, j int) bool { return events[i].CreatedAt.Before(events[j].CreatedAt) })
	return events, nil
}

// Ack removes a delivered (or permanently rejected) event. Removing a missing event
// is not an error — Ack is idempotent.
func (s *Store) Ack(jobID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path(jobID)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("ack outbox event: %w", err)
	}
	return nil
}

// Len returns the number of pending events (for logging / metrics).
func (s *Store) Len() (int, error) {
	events, err := s.List()
	if err != nil {
		return 0, err
	}
	return len(events), nil
}
