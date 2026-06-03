//go:build integration

package store_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	vaccrypto "github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// testApp inserts a unique app row for the surrounding test and returns it.
// Most Phase 2 tables FK to apps; this keeps fixture setup terse.
func testApp(t *testing.T, s *store.Store, slug string) store.App {
	t.Helper()
	ctx := context.Background()
	a, err := s.CreateApp(ctx, "Test "+slug, slug, "git@github.com:vojir-mikulas/test.git", "main", "compose.yaml", "auto", nil)
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	return a
}

func newBox(t *testing.T) *vaccrypto.Box {
	t.Helper()
	key := make([]byte, vaccrypto.KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	box, err := vaccrypto.New(key)
	if err != nil {
		t.Fatalf("crypto.New: %v", err)
	}
	return box
}

func TestAppsStatusWidened(t *testing.T) {
	// Phase 1 forbade these statuses via a CHECK constraint; 00011 drops it
	// and the Go side validates writes instead.
	s := setup(t)
	ctx := context.Background()
	a := testApp(t, s, "widen-status")

	for _, status := range []string{"building", "deploying", "running", "degraded", "crash-loop", "error", "interrupted"} {
		if err := s.SetAppStatus(ctx, a.ID, status); err != nil {
			t.Fatalf("SetAppStatus(%q): %v", status, err)
		}
		got, err := s.GetApp(ctx, a.ID)
		if err != nil {
			t.Fatalf("GetApp: %v", err)
		}
		if got.Status != status {
			t.Errorf("after SetAppStatus(%q): status=%q", status, got.Status)
		}
	}
}

func TestSSHKeysCRUD(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	box := newBox(t)
	a := testApp(t, s, "ssh-keys-app")

	if _, err := s.GetSSHKeyForApp(ctx, a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for fresh app, got %v", err)
	}

	// Encrypt a fake private key; round-trip through the DB.
	priv := []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nfakebody\n-----END-----")
	sealed, err := box.Seal(priv)
	if err != nil {
		t.Fatalf("box.Seal: %v", err)
	}
	pub := "ssh-ed25519 AAAAfake vac-key-" + a.Slug

	k, err := s.UpsertSSHKey(ctx, a.ID, pub, sealed)
	if err != nil {
		t.Fatalf("UpsertSSHKey insert: %v", err)
	}
	if k.AppID != a.ID || k.PublicKey != pub {
		t.Errorf("unexpected key: %+v", k)
	}
	if !bytes.Equal(k.PrivateKey, sealed) {
		t.Errorf("sealed private key did not round-trip via RETURNING")
	}

	got, err := s.GetSSHKeyForApp(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetSSHKeyForApp: %v", err)
	}
	opened, err := box.Open(got.PrivateKey)
	if err != nil {
		t.Fatalf("box.Open: %v", err)
	}
	if !bytes.Equal(opened, priv) {
		t.Errorf("decrypted private key mismatch: got %q want %q", opened, priv)
	}

	// Regenerate — same UpsertSSHKey path, the UNIQUE on app_id triggers UPDATE.
	priv2 := []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nrotated\n-----END-----")
	sealed2, _ := box.Seal(priv2)
	pub2 := "ssh-ed25519 AAAArotated vac-key-" + a.Slug
	k2, err := s.UpsertSSHKey(ctx, a.ID, pub2, sealed2)
	if err != nil {
		t.Fatalf("UpsertSSHKey rotate: %v", err)
	}
	if k2.ID != k.ID {
		// ON CONFLICT DO UPDATE preserves the row, so the PK is stable.
		t.Errorf("expected same row id after rotate, got %s vs %s", k2.ID, k.ID)
	}
	if k2.PublicKey != pub2 {
		t.Errorf("rotated public key not stored: %s", k2.PublicKey)
	}

	if err := s.DeleteSSHKeyForApp(ctx, a.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.GetSSHKeyForApp(ctx, a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestServicesCRUD(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	a := testApp(t, s, "services-app")

	containerID := "abc123"
	port := 8080
	internalPort := 3000
	svc, err := s.UpsertService(ctx, a.ID, "web", &containerID, &port, &internalPort, "deploying")
	if err != nil {
		t.Fatalf("UpsertService insert: %v", err)
	}
	if svc.ServiceName != "web" || *svc.ContainerID != containerID || *svc.ExposedPort != port {
		t.Errorf("unexpected: %+v", svc)
	}
	if svc.InternalPort == nil || *svc.InternalPort != internalPort {
		t.Errorf("internal_port not set: %+v", svc.InternalPort)
	}
	if svc.Status != "deploying" || svc.RestartCount != 0 {
		t.Errorf("defaults wrong: status=%s restart=%d", svc.Status, svc.RestartCount)
	}

	// Phase 2 status (would have been rejected by Phase 1's CHECK on apps).
	if err := s.UpdateServiceStatus(ctx, a.ID, "web", "crash-loop", intPtr(137)); err != nil {
		t.Fatalf("UpdateServiceStatus: %v", err)
	}
	got, _ := s.GetService(ctx, a.ID, "web")
	if got.Status != "crash-loop" || got.LastExitCode == nil || *got.LastExitCode != 137 {
		t.Errorf("after UpdateServiceStatus: %+v", got)
	}

	n, err := s.IncrementServiceRestart(ctx, a.ID, "web")
	if err != nil || n != 1 {
		t.Errorf("IncrementServiceRestart = %d, %v; want 1, nil", n, err)
	}
	n, _ = s.IncrementServiceRestart(ctx, a.ID, "web")
	if n != 2 {
		t.Errorf("second increment = %d, want 2", n)
	}

	healthPath := "/healthz"
	patched, err := s.SetServiceConfig(ctx, a.ID, "web", nil, nil, &healthPath)
	if err != nil {
		t.Fatalf("SetServiceConfig: %v", err)
	}
	if patched.HealthPath == nil || *patched.HealthPath != healthPath {
		t.Errorf("health_path not set: %+v", patched.HealthPath)
	}
	// internal_port should be preserved (COALESCE) across the partial patch.
	if patched.InternalPort == nil || *patched.InternalPort != internalPort {
		t.Errorf("internal_port clobbered by patch: %+v", patched.InternalPort)
	}

	// Add a second service for the List path.
	if _, err := s.UpsertService(ctx, a.ID, "worker", nil, nil, nil, "running"); err != nil {
		t.Fatalf("UpsertService worker: %v", err)
	}
	list, err := s.ListServicesForApp(ctx, a.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListServicesForApp = %d rows, err=%v", len(list), err)
	}
	// ORDER BY service_name → web, worker
	if list[0].ServiceName != "web" || list[1].ServiceName != "worker" {
		t.Errorf("order wrong: %s, %s", list[0].ServiceName, list[1].ServiceName)
	}

	if err := s.DeleteService(ctx, a.ID, "worker"); err != nil {
		t.Fatalf("DeleteService: %v", err)
	}
	if _, err := s.GetService(ctx, a.ID, "worker"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDeploymentsCRUD(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	a := testApp(t, s, "deployments-app")

	d, err := s.CreateDeployment(ctx, a.ID, store.TriggeredManual, nil)
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if d.Status != "queued" || d.StartedAt != nil || d.FinishedAt != nil {
		t.Errorf("fresh deploy looks wrong: %+v", d)
	}
	if d.TriggeredBy != store.TriggeredManual || d.RolledBackFrom != nil {
		t.Errorf("trigger fields wrong: by=%q from=%v", d.TriggeredBy, d.RolledBackFrom)
	}

	// Step through the lifecycle.
	if err := s.MarkDeploymentStarted(ctx, d.ID); err != nil {
		t.Fatalf("MarkDeploymentStarted: %v", err)
	}
	for _, status := range []string{"cloning", "building", "deploying", "health-checking"} {
		if err := s.UpdateDeploymentStatus(ctx, d.ID, status, nil); err != nil {
			t.Fatalf("UpdateDeploymentStatus(%s): %v", status, err)
		}
	}
	if err := s.SetDeploymentCommit(ctx, d.ID, stringPtr("deadbeefcafe"), stringPtr("first deploy")); err != nil {
		t.Fatalf("SetDeploymentCommit: %v", err)
	}
	if err := s.SetDeploymentComposeHash(ctx, d.ID, "sha256:zzz"); err != nil {
		t.Fatalf("SetDeploymentComposeHash: %v", err)
	}
	if err := s.MarkDeploymentFinished(ctx, d.ID, "running", nil); err != nil {
		t.Fatalf("MarkDeploymentFinished: %v", err)
	}

	got, err := s.GetDeployment(ctx, d.ID)
	if err != nil {
		t.Fatalf("GetDeployment: %v", err)
	}
	if got.Status != "running" || got.StartedAt == nil || got.FinishedAt == nil {
		t.Errorf("end-state wrong: %+v", got)
	}
	if got.CommitSHA == nil || *got.CommitSHA != "deadbeefcafe" {
		t.Errorf("commit sha not persisted: %+v", got.CommitSHA)
	}
	if got.ComposeHash == nil || *got.ComposeHash != "sha256:zzz" {
		t.Errorf("compose hash not persisted: %+v", got.ComposeHash)
	}

	list, err := s.ListDeploymentsForApp(ctx, a.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListDeploymentsForApp = %d, err=%v", len(list), err)
	}

	// MarkInProgressDeploymentsInterrupted should be a no-op now (the row
	// is terminal). Create a fresh queued row and run the sweep.
	stranded, err := s.CreateDeployment(ctx, a.ID, store.TriggeredManual, nil)
	if err != nil {
		t.Fatalf("CreateDeployment 2: %v", err)
	}
	if err := s.UpdateDeploymentStatus(ctx, stranded.ID, "building", nil); err != nil {
		t.Fatalf("Update to building: %v", err)
	}
	n, err := s.MarkInProgressDeploymentsInterrupted(ctx)
	if err != nil {
		t.Fatalf("MarkInProgressDeploymentsInterrupted: %v", err)
	}
	if n != 1 {
		t.Errorf("swept = %d, want 1", n)
	}
	got2, _ := s.GetDeployment(ctx, stranded.ID)
	if got2.Status != "interrupted" || got2.FinishedAt == nil {
		t.Errorf("stranded not marked interrupted: %+v", got2)
	}
}

func TestCreateRollbackDeployment(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	a := testApp(t, s, "rollback-app")

	// A successful source deployment to roll back to.
	src, err := s.CreateDeployment(ctx, a.ID, store.TriggeredManual, nil)
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := s.SetDeploymentCommit(ctx, src.ID, stringPtr("c0ffee1234"), stringPtr("good build")); err != nil {
		t.Fatalf("SetDeploymentCommit: %v", err)
	}
	if err := s.MarkDeploymentFinished(ctx, src.ID, "running", nil); err != nil {
		t.Fatalf("MarkDeploymentFinished: %v", err)
	}

	// Happy path: the rollback row points back at the source and pre-seeds its
	// commit so the pipeline can pin to it.
	rb, err := s.CreateRollbackDeployment(ctx, a.ID, src.ID)
	if err != nil {
		t.Fatalf("CreateRollbackDeployment: %v", err)
	}
	if rb.TriggeredBy != store.TriggeredRollback {
		t.Errorf("triggered_by = %q, want rollback", rb.TriggeredBy)
	}
	if rb.RolledBackFrom == nil || *rb.RolledBackFrom != src.ID {
		t.Errorf("rolled_back_from = %v, want %q", rb.RolledBackFrom, src.ID)
	}
	if rb.CommitSHA == nil || *rb.CommitSHA != "c0ffee1234" {
		t.Errorf("commit_sha = %v, want copied from source", rb.CommitSHA)
	}
	if rb.Status != "queued" {
		t.Errorf("status = %q, want queued", rb.Status)
	}

	// Unknown source → ErrNotFound.
	if _, err := s.CreateRollbackDeployment(ctx, a.ID, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown source err = %v, want ErrNotFound", err)
	}

	// A failed source is not a valid rollback target.
	bad, err := s.CreateDeployment(ctx, a.ID, store.TriggeredManual, nil)
	if err != nil {
		t.Fatalf("CreateDeployment(bad): %v", err)
	}
	if err := s.MarkDeploymentFinished(ctx, bad.ID, "error", stringPtr("boom")); err != nil {
		t.Fatalf("MarkDeploymentFinished(bad): %v", err)
	}
	if _, err := s.CreateRollbackDeployment(ctx, a.ID, bad.ID); !errors.Is(err, store.ErrRollbackSourceInvalid) {
		t.Errorf("failed-source err = %v, want ErrRollbackSourceInvalid", err)
	}

	// A successful deployment that belongs to another app is rejected.
	other := testApp(t, s, "rollback-other")
	if _, err := s.CreateRollbackDeployment(ctx, other.ID, src.ID); !errors.Is(err, store.ErrRollbackSourceInvalid) {
		t.Errorf("cross-app err = %v, want ErrRollbackSourceInvalid", err)
	}

	// A successful source with no recorded commit can't be pinned (it would
	// rebuild branch HEAD under a rollback label), so it's rejected too.
	noCommit, err := s.CreateDeployment(ctx, a.ID, store.TriggeredManual, nil)
	if err != nil {
		t.Fatalf("CreateDeployment(noCommit): %v", err)
	}
	if err := s.MarkDeploymentFinished(ctx, noCommit.ID, "running", nil); err != nil {
		t.Fatalf("MarkDeploymentFinished(noCommit): %v", err)
	}
	if _, err := s.CreateRollbackDeployment(ctx, a.ID, noCommit.ID); !errors.Is(err, store.ErrRollbackSourceInvalid) {
		t.Errorf("no-commit err = %v, want ErrRollbackSourceInvalid", err)
	}
}

func TestAppWebhookSecretRoundTrip(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	a := testApp(t, s, "webhook-secret-app")

	// Unset by default.
	enc, err := s.GetAppWebhookSecret(ctx, a.ID)
	if err != nil {
		t.Fatalf("GetAppWebhookSecret: %v", err)
	}
	if enc != nil {
		t.Errorf("fresh app webhook secret = %v, want nil", enc)
	}

	// Set, read back, clear.
	if err := s.SetAppWebhookSecret(ctx, a.ID, []byte("sealed-bytes")); err != nil {
		t.Fatalf("SetAppWebhookSecret: %v", err)
	}
	enc, err = s.GetAppWebhookSecret(ctx, a.ID)
	if err != nil || string(enc) != "sealed-bytes" {
		t.Fatalf("GetAppWebhookSecret = (%q,%v)", enc, err)
	}
	if err := s.SetAppWebhookSecret(ctx, a.ID, nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if enc, _ := s.GetAppWebhookSecret(ctx, a.ID); enc != nil {
		t.Errorf("after clear = %v, want nil", enc)
	}

	// Unknown app → ErrNotFound on both read and write.
	if _, err := s.GetAppWebhookSecret(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("get unknown app err = %v, want ErrNotFound", err)
	}
	if err := s.SetAppWebhookSecret(ctx, "00000000-0000-0000-0000-000000000000", []byte("x")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("set unknown app err = %v, want ErrNotFound", err)
	}
}

func TestHasActiveDeployment(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	a := testApp(t, s, "active-deploy-app")

	if active, err := s.HasActiveDeployment(ctx, a.ID); err != nil || active {
		t.Fatalf("no deploys: active=%v err=%v, want false", active, err)
	}

	d, err := s.CreateDeployment(ctx, a.ID, store.TriggeredManual, nil)
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	// A freshly queued deploy is active.
	if active, _ := s.HasActiveDeployment(ctx, a.ID); !active {
		t.Error("queued deploy should be active")
	}
	// Mid-pipeline is active.
	if err := s.UpdateDeploymentStatus(ctx, d.ID, "building", nil); err != nil {
		t.Fatalf("UpdateDeploymentStatus: %v", err)
	}
	if active, _ := s.HasActiveDeployment(ctx, a.ID); !active {
		t.Error("building deploy should be active")
	}
	// Terminal is not active.
	if err := s.MarkDeploymentFinished(ctx, d.ID, "running", nil); err != nil {
		t.Fatalf("MarkDeploymentFinished: %v", err)
	}
	if active, _ := s.HasActiveDeployment(ctx, a.ID); active {
		t.Error("finished deploy should not be active")
	}
}

func TestDeploymentLogsAppendAndList(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	a := testApp(t, s, "deploy-logs-app")
	d, err := s.CreateDeployment(ctx, a.ID, store.TriggeredManual, nil)
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	if _, err := s.AppendDeploymentLogs(ctx, d.ID, nil); err != nil {
		t.Errorf("empty append should be a no-op: %v", err)
	}

	web := "web"
	rows := []store.DeploymentLogRow{
		{ServiceName: nil, Stream: store.DeploymentLogStreamSystem, Message: "pipeline: starting"},
		{ServiceName: &web, Stream: store.DeploymentLogStreamStdout, Message: "step 1/5"},
		{ServiceName: &web, Stream: store.DeploymentLogStreamStdout, Message: "step 2/5"},
		{ServiceName: &web, Stream: store.DeploymentLogStreamStderr, Message: "warning: layer cache miss"},
	}
	if ids, err := s.AppendDeploymentLogs(ctx, d.ID, rows); err != nil {
		t.Fatalf("AppendDeploymentLogs: %v", err)
	} else if len(ids) != len(rows) {
		t.Fatalf("AppendDeploymentLogs returned %d ids, want %d", len(ids), len(rows))
	}

	n, _ := s.CountDeploymentLogs(ctx, d.ID)
	if n != 4 {
		t.Errorf("CountDeploymentLogs = %d, want 4", n)
	}

	page1, err := s.ListDeploymentLogs(ctx, d.ID, 0, 2)
	if err != nil {
		t.Fatalf("ListDeploymentLogs page 1: %v", err)
	}
	if len(page1) != 2 || page1[0].Message != "pipeline: starting" {
		t.Errorf("page 1 wrong: %+v", page1)
	}
	page2, err := s.ListDeploymentLogs(ctx, d.ID, page1[1].ID, 10)
	if err != nil {
		t.Fatalf("ListDeploymentLogs page 2: %v", err)
	}
	if len(page2) != 2 || page2[1].Message != "warning: layer cache miss" {
		t.Errorf("page 2 wrong: %+v", page2)
	}
	if page2[0].ServiceName == nil || *page2[0].ServiceName != "web" {
		t.Errorf("service_name not preserved: %+v", page2[0].ServiceName)
	}
}

func TestRuntimeLogsAppendListPrune(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	a := testApp(t, s, "runtime-logs-app")

	rows := []store.RuntimeLogRow{
		{ServiceName: "web", Stream: store.RuntimeLogStreamStdout, Message: "hello"},
		{ServiceName: "web", Stream: store.RuntimeLogStreamStderr, Message: "warn"},
		{ServiceName: "worker", Stream: store.RuntimeLogStreamStdout, Message: "tick"},
	}
	if _, err := s.AppendRuntimeLogs(ctx, a.ID, rows); err != nil {
		t.Fatalf("AppendRuntimeLogs: %v", err)
	}

	all, err := s.ListRuntimeLogs(ctx, a.ID, "", 0, 10)
	if err != nil {
		t.Fatalf("ListRuntimeLogs all: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("len(all) = %d, want 3", len(all))
	}

	web, err := s.ListRuntimeLogs(ctx, a.ID, "web", 0, 10)
	if err != nil {
		t.Fatalf("ListRuntimeLogs web: %v", err)
	}
	if len(web) != 2 {
		t.Errorf("len(web) = %d, want 2", len(web))
	}

	// Prune everything older than now+1s — should delete all three.
	cutoff := time.Now().Add(1 * time.Second)
	deleted, err := s.DeleteRuntimeLogsOlderThan(ctx, cutoff)
	if err != nil {
		t.Fatalf("DeleteRuntimeLogsOlderThan: %v", err)
	}
	if deleted != 3 {
		t.Errorf("deleted = %d, want 3", deleted)
	}
	n, _ := s.CountRuntimeLogs(ctx, a.ID)
	if n != 0 {
		t.Errorf("post-prune count = %d, want 0", n)
	}
}

func TestEnvVarsReplaceAll(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	box := newBox(t)
	a := testApp(t, s, "env-vars-app")

	sealedHello, _ := box.Seal([]byte("hello"))
	sealedWorld, _ := box.Seal([]byte("world"))
	first := []store.EnvVarInput{
		{Key: "GREETING", Value: sealedHello, Sensitive: false},
		{Key: "TARGET", Value: sealedWorld, Sensitive: true},
	}
	if err := s.ReplaceEnvVars(ctx, a.ID, first); err != nil {
		t.Fatalf("ReplaceEnvVars first: %v", err)
	}

	got, err := s.ListEnvVarsForApp(ctx, a.ID)
	if err != nil {
		t.Fatalf("ListEnvVarsForApp: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	// ORDER BY key → GREETING, TARGET
	if got[0].Key != "GREETING" || got[1].Key != "TARGET" {
		t.Errorf("order wrong: %s, %s", got[0].Key, got[1].Key)
	}
	openedHello, err := box.Open(got[0].Value)
	if err != nil || string(openedHello) != "hello" {
		t.Errorf("decrypted GREETING = %q, err=%v", openedHello, err)
	}
	// Sensitivity flag round-trips per row.
	if got[0].Sensitive {
		t.Errorf("GREETING should be non-sensitive")
	}
	if !got[1].Sensitive {
		t.Errorf("TARGET should be sensitive")
	}

	// Replace-all semantics — pre-existing rows disappear.
	sealedNew, _ := box.Seal([]byte("v2"))
	second := []store.EnvVarInput{{Key: "ONLY_ONE", Value: sealedNew}}
	if err := s.ReplaceEnvVars(ctx, a.ID, second); err != nil {
		t.Fatalf("ReplaceEnvVars second: %v", err)
	}
	got2, _ := s.ListEnvVarsForApp(ctx, a.ID)
	if len(got2) != 1 || got2[0].Key != "ONLY_ONE" {
		t.Errorf("replace-all not honored: %+v", got2)
	}
	v, err := s.GetEnvVar(ctx, a.ID, "ONLY_ONE")
	if err != nil {
		t.Fatalf("GetEnvVar: %v", err)
	}
	opened, _ := box.Open(v.Value)
	if string(opened) != "v2" {
		t.Errorf("decrypted ONLY_ONE = %q, want v2", opened)
	}

	if _, err := s.GetEnvVar(ctx, a.ID, "GREETING"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound for purged GREETING, got %v", err)
	}
}

// TestEnvVarsWriteOnlyRoundTrip locks in P6.1: the write_only flag survives
// ReplaceEnvVars → ListEnvVarsForApp / GetEnvVar, defaulting to false for rows
// that don't set it.
func TestEnvVarsWriteOnlyRoundTrip(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	box := newBox(t)
	a := testApp(t, s, "env-write-only-app")

	sealedSecret, _ := box.Seal([]byte("s3cr3t"))
	sealedPlain, _ := box.Seal([]byte("plain"))
	in := []store.EnvVarInput{
		{Key: "API_TOKEN", Value: sealedSecret, Sensitive: true, WriteOnly: true},
		{Key: "LOG_LEVEL", Value: sealedPlain, Sensitive: false, WriteOnly: false},
	}
	if err := s.ReplaceEnvVars(ctx, a.ID, in); err != nil {
		t.Fatalf("ReplaceEnvVars: %v", err)
	}

	got, err := s.ListEnvVarsForApp(ctx, a.ID)
	if err != nil {
		t.Fatalf("ListEnvVarsForApp: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	// ORDER BY key → API_TOKEN, LOG_LEVEL.
	if !got[0].WriteOnly {
		t.Errorf("API_TOKEN should be write-only")
	}
	if got[1].WriteOnly {
		t.Errorf("LOG_LEVEL should not be write-only")
	}
	// Sealed bytes still round-trip for a write-only row (it's set/replaceable).
	opened, err := box.Open(got[0].Value)
	if err != nil || string(opened) != "s3cr3t" {
		t.Errorf("decrypt API_TOKEN = %q err=%v", opened, err)
	}

	v, err := s.GetEnvVar(ctx, a.ID, "API_TOKEN")
	if err != nil {
		t.Fatalf("GetEnvVar: %v", err)
	}
	if !v.WriteOnly {
		t.Errorf("GetEnvVar API_TOKEN write_only = false, want true")
	}
}

func stringPtr(s string) *string { return &s }
func intPtr(i int) *int          { return &i }
