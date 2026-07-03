package agent

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	agentclient "github.com/cerberus8484/opensourcebackup/internal/agent/client"
	"github.com/cerberus8484/opensourcebackup/internal/agent/outbox"
)

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// deliverFake implements just the terminal methods of ControlPlaneClient by
// embedding the interface (other methods stay nil and would panic if called —
// tryDeliver only invokes the terminal ones).
type deliverFake struct {
	ControlPlaneClient
	err   error
	calls int
}

func (f *deliverFake) CompleteJob(context.Context, uuid.UUID, string, int64, []string) error {
	f.calls++
	return f.err
}
func (f *deliverFake) FailJob(context.Context, uuid.UUID, string) error      { f.calls++; return f.err }
func (f *deliverFake) CancelledJob(context.Context, uuid.UUID, string) error { f.calls++; return f.err }

func newAgentWithOutbox(t *testing.T, cp ControlPlaneClient) (*Agent, *outbox.Store) {
	t.Helper()
	ob, err := outbox.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("outbox: %v", err)
	}
	return &Agent{cp: cp, log: quietLogger(), outbox: ob}, ob
}

func pending(t *testing.T, ob *outbox.Store) int {
	t.Helper()
	n, err := ob.Len()
	if err != nil {
		t.Fatalf("outbox len: %v", err)
	}
	return n
}

// The delivery guarantee: a successfully delivered terminal event is acked and the
// outbox is emptied.
func TestTryDeliver_AcksOnSuccess(t *testing.T) {
	a, ob := newAgentWithOutbox(t, &deliverFake{err: nil})
	e := outbox.Event{JobID: uuid.New(), Kind: outbox.KindComplete}
	if err := ob.Enqueue(e); err != nil {
		t.Fatal(err)
	}
	a.tryDeliver(context.Background(), a.log, e)
	if got := pending(t, ob); got != 0 {
		t.Errorf("want empty outbox after successful delivery, got %d", got)
	}
}

// A permanent rejection (the control plane already has a terminal state) drops the
// event instead of retrying forever.
func TestTryDeliver_DropsOnPermanentConflict(t *testing.T) {
	a, ob := newAgentWithOutbox(t, &deliverFake{err: agentclient.ErrConflict})
	e := outbox.Event{JobID: uuid.New(), Kind: outbox.KindFail, Reason: "x"}
	_ = ob.Enqueue(e)
	a.tryDeliver(context.Background(), a.log, e)
	if got := pending(t, ob); got != 0 {
		t.Errorf("permanent conflict must drop the event, got %d remaining", got)
	}
}

// The heart of the trust guarantee: a transient failure (control plane briefly
// unreachable) leaves the event in the outbox so it is retried — never lost.
func TestTryDeliver_KeepsOnTransientFailure(t *testing.T) {
	a, ob := newAgentWithOutbox(t, &deliverFake{err: errors.New("connection refused")})
	e := outbox.Event{JobID: uuid.New(), Kind: outbox.KindComplete}
	_ = ob.Enqueue(e)
	a.tryDeliver(context.Background(), a.log, e)
	if got := pending(t, ob); got != 1 {
		t.Errorf("transient failure must keep the event for retry, got %d", got)
	}
}

// reportTerminal persists first, then delivers — so the event exists on disk even
// if delivery races with a crash.
func TestReportTerminal_PersistsThenDelivers(t *testing.T) {
	fake := &deliverFake{err: nil}
	a, ob := newAgentWithOutbox(t, fake)
	a.reportTerminal(context.Background(), a.log,
		outbox.Event{JobID: uuid.New(), Kind: outbox.KindComplete})
	if fake.calls != 1 {
		t.Errorf("expected one delivery attempt, got %d", fake.calls)
	}
	if got := pending(t, ob); got != 0 {
		t.Errorf("delivered event should be acked, got %d remaining", got)
	}
}

func TestIsPermanent(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"conflict", agentclient.ErrConflict, true},
		{"not found", agentclient.ErrNotFound, true},
		{"unauthorized", agentclient.ErrUnauthorized, true},
		{"network", errors.New("connection refused"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := isPermanent(c.err); got != c.want {
			t.Errorf("%s: isPermanent = %v, want %v", c.name, got, c.want)
		}
	}
}
