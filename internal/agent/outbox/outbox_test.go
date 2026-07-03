package outbox

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestStore_EnqueueListAck(t *testing.T) {
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	e := Event{JobID: uuid.New(), Kind: KindComplete, SnapshotID: "snap1", Bytes: 4096, Paths: []string{"/etc"}}

	if err := s.Enqueue(e); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	got, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 event, got %d", len(got))
	}
	if got[0].JobID != e.JobID || got[0].Kind != KindComplete || got[0].SnapshotID != "snap1" || got[0].Bytes != 4096 {
		t.Errorf("event round-trip mismatch: %+v", got[0])
	}
	if got[0].CreatedAt.IsZero() {
		t.Error("CreatedAt should be stamped on Enqueue")
	}

	if err := s.Ack(e.JobID); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	got, _ = s.List()
	if len(got) != 0 {
		t.Errorf("want 0 events after Ack, got %d", len(got))
	}
}

// The whole point: an event survives the process restarting (a fresh Store on the
// same directory still sees it). This is what makes terminal outcomes crash-proof.
func TestStore_SurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	s1, _ := NewStore(dir)
	id := uuid.New()
	if err := s1.Enqueue(Event{JobID: id, Kind: KindFail, Reason: "restic exit 3"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	s2, err := NewStore(dir) // simulates an agent restart
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, _ := s2.List()
	if len(got) != 1 || got[0].JobID != id || got[0].Reason != "restic exit 3" {
		t.Fatalf("event did not survive reopen: %+v", got)
	}
}

// A job has exactly one terminal outcome; re-enqueuing the same job overwrites
// rather than duplicating.
func TestStore_OneFilePerJob(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	id := uuid.New()
	_ = s.Enqueue(Event{JobID: id, Kind: KindComplete})
	_ = s.Enqueue(Event{JobID: id, Kind: KindFail, Reason: "later truth"})

	got, _ := s.List()
	if len(got) != 1 {
		t.Fatalf("want 1 event (deduped by job), got %d", len(got))
	}
	if got[0].Kind != KindFail || got[0].Reason != "later truth" {
		t.Errorf("latest outcome should win, got %+v", got[0])
	}
}

func TestStore_AckMissingIsNoError(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	if err := s.Ack(uuid.New()); err != nil {
		t.Errorf("Ack of missing event should be a no-op, got %v", err)
	}
}

func TestStore_ListOrdersByCreatedAt(t *testing.T) {
	s, _ := NewStore(t.TempDir())
	older := Event{JobID: uuid.New(), Kind: KindComplete, CreatedAt: time.Unix(1000, 0)}
	newer := Event{JobID: uuid.New(), Kind: KindComplete, CreatedAt: time.Unix(2000, 0)}
	_ = s.Enqueue(newer)
	_ = s.Enqueue(older)

	got, _ := s.List()
	if len(got) != 2 || !got[0].CreatedAt.Equal(older.CreatedAt) {
		t.Errorf("List should return oldest first, got %+v", got)
	}
}
