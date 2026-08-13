//go:build e2e

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	root "github.com/cerberus8484/opensourcebackup"
	"github.com/cerberus8484/opensourcebackup/internal/agent"
	agentclient "github.com/cerberus8484/opensourcebackup/internal/agent/client"
	"github.com/cerberus8484/opensourcebackup/internal/api"
	"github.com/cerberus8484/opensourcebackup/internal/audit"
	"github.com/cerberus8484/opensourcebackup/internal/auth"
	"github.com/cerberus8484/opensourcebackup/internal/catalog"
	"github.com/cerberus8484/opensourcebackup/internal/migrate"
)

var (
	migrateOnce sync.Once
	migrateErr  error
)

// TestE14DispatchFullPath proves the production HTTP handler, bearer-authenticated
// agent client, agent runner, durable outbox and an executable fake-restic process
// work together. DATABASE_URL must refer to a disposable PostgreSQL database.
func TestE14DispatchFullPath(t *testing.T) {
	db := openE2EDB(t)
	truncateE2E(t, db)

	fakeBin, fakeLog := buildFakeRestic(t)
	server := newControlPlane(t, db, catalog.DispatchLimits{Global: 2, PerRepository: 1, PerAgent: 1})
	defer server.Close()

	t.Run("repository backpressure resumes through agent polling", func(t *testing.T) {
		repo := createRepo(t, db, "repo-backpressure")
		a := createSystemAndToken(t, db, "repo-a")
		b := createSystemAndToken(t, db, "repo-b")
		policy := createPolicy(t, db, repo.ID)
		j1 := createJob(t, db, a.system.ID, policy.ID)
		j2 := createJob(t, db, b.system.ID, policy.ID)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		outboxA := runAgent(ctx, t, server.URL, a.token, fakeBin)
		outboxB := runAgent(ctx, t, server.URL, b.token, fakeBin)
		waitFor(t, 5*time.Second, func() bool { return status(t, db, j1.ID) == "running" || status(t, db, j2.ID) == "running" })
		waitFor(t, 8*time.Second, func() bool { return status(t, db, j1.ID) == "success" && status(t, db, j2.ID) == "success" })
		assertSnapshots(t, db, j1.ID, j2.ID)
		assertOutboxEmpty(t, outboxA, outboxB)
	})

	t.Run("global backpressure resumes after a slot is released", func(t *testing.T) {
		agents := make([]agentIdentity, 3)
		jobs := make([]catalog.BackupJob, 3)
		for i := range agents {
			agents[i] = createSystemAndToken(t, db, fmt.Sprintf("global-%d", i))
			repo := createRepo(t, db, fmt.Sprintf("global-repo-%d", i))
			policy := createPolicy(t, db, repo.ID)
			jobs[i] = createJob(t, db, agents[i].system.ID, policy.ID)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		for _, identity := range agents {
			runAgent(ctx, t, server.URL, identity.token, fakeBin)
		}
		waitFor(t, 5*time.Second, func() bool {
			return runningCount(t, db) == 2 && pendingCount(t, db, jobs) == 1
		})
		waitFor(t, 10*time.Second, func() bool {
			for _, job := range jobs {
				if status(t, db, job.ID) != "success" {
					return false
				}
			}
			return true
		})
	})

	t.Run("same agent remains capped when two real agent runners share its token", func(t *testing.T) {
		repo := createRepo(t, db, "agent-cap")
		identity := createSystemAndToken(t, db, "agent-cap")
		policy := createPolicy(t, db, repo.ID)
		first := createJob(t, db, identity.system.ID, policy.ID)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		runAgent(ctx, t, server.URL, identity.token, fakeBin)
		waitFor(t, 5*time.Second, func() bool { return status(t, db, first.ID) == "running" })
		second := createJob(t, db, identity.system.ID, policy.ID)
		runAgent(ctx, t, server.URL, identity.token, fakeBin)
		waitFor(t, 5*time.Second, func() bool {
			return status(t, db, second.ID) == "pending" && runningCountForSystem(t, db, identity.system.ID) == 1
		})
		waitFor(t, 8*time.Second, func() bool { return status(t, db, first.ID) == "success" && status(t, db, second.ID) == "success" })
	})

	t.Run("HTTP dispatch reports deterministic global repository agent priority", func(t *testing.T) {
		candidate := createSystemAndToken(t, db, "priority-candidate")
		other := createSystemAndToken(t, db, "priority-other")
		repoA := createRepo(t, db, "priority-a")
		repoB := createRepo(t, db, "priority-b")
		policyA := createPolicy(t, db, repoA.ID)
		policyB := createPolicy(t, db, repoB.ID)
		first := createJob(t, db, candidate.system.ID, policyA.ID)
		second := createJob(t, db, other.system.ID, policyB.ID)
		blocked := createJob(t, db, candidate.system.ID, policyA.ID)
		candidateClient := agentclient.New(server.URL, candidate.token)
		otherClient := agentclient.New(server.URL, other.token)
		if err := candidateClient.StartJob(context.Background(), first.ID); err != nil {
			t.Fatalf("start first: %v", err)
		}
		if err := otherClient.StartJob(context.Background(), second.ID); err != nil {
			t.Fatalf("start second: %v", err)
		}
		if got := dispatchBlockReason(t, server.URL, candidate.token, blocked.ID); got != catalog.DispatchBlockedGlobalConcurrency {
			t.Fatalf("global reason = %q", got)
		}
		if err := otherClient.CompleteJob(context.Background(), second.ID, "priority-global", 1, nil); err != nil {
			t.Fatalf("complete global slot: %v", err)
		}
		if got := dispatchBlockReason(t, server.URL, candidate.token, blocked.ID); got != catalog.DispatchBlockedRepositoryConcurrency {
			t.Fatalf("repository reason = %q", got)
		}
		if err := candidateClient.CompleteJob(context.Background(), first.ID, "priority-repository", 1, nil); err != nil {
			t.Fatalf("complete repository slot: %v", err)
		}
		agentOnly := createJob(t, db, candidate.system.ID, policyB.ID)
		if err := candidateClient.StartJob(context.Background(), agentOnly.ID); err != nil {
			t.Fatalf("start agent-only holder: %v", err)
		}
		if got := dispatchBlockReason(t, server.URL, candidate.token, blocked.ID); got != catalog.DispatchBlockedAgentConcurrency {
			t.Fatalf("agent reason = %q", got)
		}
	})

	if count := fakeBackupCount(t, fakeLog); count != 7 {
		t.Fatalf("fake-restic backups = %d, want 7; expected one process per successful job", count)
	}
}

type agentIdentity struct {
	system catalog.System
	token  string
}

func openE2EDB(t *testing.T) *catalog.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	migrateOnce.Do(func() {
		migrateErr = migrate.Run(context.Background(), dsn, root.MigrationsFS, slog.New(slog.NewTextHandler(io.Discard, nil)))
	})
	if migrateErr != nil {
		t.Fatalf("migrate e2e database: %v", migrateErr)
	}
	db, err := catalog.Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open e2e database: %v", err)
	}
	t.Cleanup(db.Close)
	return db
}

func truncateE2E(t *testing.T, db *catalog.DB) {
	t.Helper()
	_, err := db.Pool().Exec(context.Background(), "TRUNCATE backup_jobs, snapshots, backup_policies, repositories, agent_tokens, systems CASCADE")
	if err != nil {
		t.Fatalf("truncate e2e data: %v", err)
	}
}

func newControlPlane(t *testing.T, db *catalog.DB, limits catalog.DispatchLimits) *httptest.Server {
	t.Helper()
	h := api.New(catalog.NewSystemStore(db), catalog.NewRepositoryStore(db), catalog.NewPolicyStore(db), catalog.NewJobStore(db), catalog.NewSnapshotStore(db), catalog.NewRestoreTestStore(db), auth.NewEnrollmentTokenStore(db), auth.NewAgentTokenStore(db), audit.NoopStore{}, slog.New(slog.NewTextHandler(io.Discard, nil))).WithDispatchGuard(catalog.NewDispatchGuardWithLimits(db, limits))
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return httptest.NewServer(mux)
}

func buildFakeRestic(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-restic")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "../../cmd/fake-restic")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake-restic: %v: %s", err, out)
	}
	log := filepath.Join(dir, "fake-restic.log")
	t.Setenv("FAKE_RESTIC_LOG", log)
	t.Setenv("FAKE_RESTIC_SLEEP_MS", "450")
	return bin, log
}

func runAgent(ctx context.Context, t *testing.T, baseURL, token, resticBin string) string {
	t.Helper()
	outboxDir := t.TempDir()
	a := agent.New(agent.Config{PollInterval: 75 * time.Millisecond, ResticBin: resticBin, ResticPassword: "e2e-only", ResticRepo: "fake://unused", OutboxDir: outboxDir}, agentclient.New(baseURL, token), slog.New(slog.NewTextHandler(io.Discard, nil)))
	go func() { _ = a.Run(ctx) }()
	return outboxDir
}

func createSystemAndToken(t *testing.T, db *catalog.DB, hostname string) agentIdentity {
	t.Helper()
	now := time.Now().UTC()
	system := catalog.System{Hostname: hostname + "-" + uuid.NewString()[:8], RiskClass: "standard", LastSeen: &now}
	if err := catalog.NewSystemStore(db).Create(context.Background(), &system); err != nil {
		t.Fatalf("create system: %v", err)
	}
	token := "e2e-" + uuid.NewString()
	if _, err := auth.NewAgentTokenStore(db).Create(context.Background(), system.ID, auth.HashToken(token)); err != nil {
		t.Fatalf("create token: %v", err)
	}
	return agentIdentity{system: system, token: token}
}

func createRepo(t *testing.T, db *catalog.DB, name string) catalog.BackupRepository {
	t.Helper()
	repo := catalog.BackupRepository{Type: "local", Location: "fake://" + name + "-" + uuid.NewString()}
	if err := catalog.NewRepositoryStore(db).Create(context.Background(), &repo); err != nil {
		t.Fatalf("create repository: %v", err)
	}
	return repo
}
func createPolicy(t *testing.T, db *catalog.DB, repoID uuid.UUID) catalog.BackupPolicy {
	t.Helper()
	policy := catalog.BackupPolicy{Name: "e2e-" + uuid.NewString(), Engine: "restic", Includes: []string{"/test"}, RepositoryID: &repoID}
	if err := catalog.NewPolicyStore(db).Create(context.Background(), &policy); err != nil {
		t.Fatalf("create policy: %v", err)
	}
	return policy
}
func createJob(t *testing.T, db *catalog.DB, systemID, policyID uuid.UUID) catalog.BackupJob {
	t.Helper()
	job := catalog.BackupJob{SystemID: systemID, PolicyID: policyID, Status: "pending"}
	if err := catalog.NewJobStore(db).Create(context.Background(), &job); err != nil {
		t.Fatalf("create job: %v", err)
	}
	return job
}
func status(t *testing.T, db *catalog.DB, id uuid.UUID) string {
	t.Helper()
	job, err := catalog.NewJobStore(db).GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	return job.Status
}
func runningCount(t *testing.T, db *catalog.DB) int {
	t.Helper()
	var n int
	if err := db.Pool().QueryRow(context.Background(), "SELECT count(*) FROM backup_jobs WHERE status='running'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func runningCountForSystem(t *testing.T, db *catalog.DB, id uuid.UUID) int {
	t.Helper()
	var n int
	if err := db.Pool().QueryRow(context.Background(), "SELECT count(*) FROM backup_jobs WHERE status='running' AND system_id=$1", id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func pendingCount(t *testing.T, db *catalog.DB, jobs []catalog.BackupJob) int {
	t.Helper()
	n := 0
	for _, job := range jobs {
		if status(t, db, job.ID) == "pending" {
			n++
		}
	}
	return n
}
func assertSnapshots(t *testing.T, db *catalog.DB, jobs ...uuid.UUID) {
	t.Helper()
	store := catalog.NewSnapshotStore(db)
	for _, id := range jobs {
		waitFor(t, 2*time.Second, func() bool {
			snapshots, err := store.ListByJobID(context.Background(), id)
			return err == nil && len(snapshots) == 1
		})
	}
}
func assertOutboxEmpty(t *testing.T, dirs ...string) {
	t.Helper()
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatalf("outbox %q entries=%d err=%v", dir, len(entries), err)
		}
	}
}
func fakeBackupCount(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fake-restic log: %v", err)
	}
	return strings.Count(string(b), " backup\n")
}

func dispatchBlockReason(t *testing.T, baseURL, token string, jobID uuid.UUID) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, baseURL+"/v1/agent/jobs/"+jobID.String()+"/start", nil)
	if err != nil {
		t.Fatalf("create dispatch request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("dispatch request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("dispatch status = %d, want 429", resp.StatusCode)
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode dispatch response: %v", err)
	}
	return body.Reason
}
func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		if condition() {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("timed out waiting for E2E condition")
		case <-tick.C:
		}
	}
}
