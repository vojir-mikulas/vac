package revert

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// fakeStore is an in-memory Store for exercising the reverter without Postgres.
type fakeStore struct {
	entry         store.AuditLog
	getErr        error
	markErr       error
	marked        string
	envApplied    []store.EnvVarInput
	envAppID      string
	baseDomain    string
	baseSet       bool
	appPatch      bool
	appName       *string
	appMem        *int
	updateErr     error
	replaceEnvErr error
}

func (f *fakeStore) GetAuditLog(_ context.Context, _ string) (store.AuditLog, error) {
	return f.entry, f.getErr
}

func (f *fakeStore) MarkAuditReverted(_ context.Context, id string) error {
	f.marked = id
	return f.markErr
}

func (f *fakeStore) ReplaceEnvVars(_ context.Context, appID string, vars []store.EnvVarInput) error {
	f.envAppID = appID
	f.envApplied = vars
	return f.replaceEnvErr
}

func (f *fakeStore) GetApp(_ context.Context, _ string) (store.App, error) {
	return store.App{}, nil
}

func (f *fakeStore) UpdateApp(_ context.Context, _ string, name, _, _, _, _ *string, _ json.RawMessage, mem, _ *int) (store.App, error) {
	f.appPatch = true
	f.appName = name
	f.appMem = mem
	return store.App{}, f.updateErr
}

func (f *fakeStore) SetBaseDomain(_ context.Context, baseDomain string) error {
	f.baseDomain = baseDomain
	f.baseSet = true
	return nil
}

type fakeBaseDom struct{ set string }

func (f *fakeBaseDom) SetBaseDomain(v string) { f.set = v }

// meta builds an audit metadata blob with a before-snapshot, as the handlers do.
func meta(t *testing.T, before map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"before": before})
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	return raw
}

func strptr(s string) *string { return &s }

func TestRevertEnv(t *testing.T) {
	sealed := []byte{0x01, 0x02, 0x03}
	woSealed := []byte{0x04, 0x05}
	before := map[string]any{"env": []map[string]any{
		{"key": "FOO", "value": base64.StdEncoding.EncodeToString(sealed), "sensitive": true},
		{"key": "SECRET", "value": base64.StdEncoding.EncodeToString(woSealed), "sensitive": true, "write_only": true},
	}}
	appID := "app-1"
	f := &fakeStore{entry: store.AuditLog{
		ID:         "a1",
		Action:     "PUT /api/apps/{id}/env",
		TargetID:   &appID,
		Revertable: true,
		Metadata:   meta(t, before),
	}}
	rv := New(f, nil)
	summary, err := rv.Revert(context.Background(), "a1")
	if err != nil {
		t.Fatalf("revert: %v", err)
	}
	if f.envAppID != appID {
		t.Errorf("env applied to %q, want %q", f.envAppID, appID)
	}
	if len(f.envApplied) != 2 || f.envApplied[0].Key != "FOO" || !f.envApplied[0].Sensitive {
		t.Fatalf("unexpected env applied: %+v", f.envApplied)
	}
	if string(f.envApplied[0].Value) != string(sealed) {
		t.Errorf("sealed bytes not round-tripped: %v", f.envApplied[0].Value)
	}
	if f.envApplied[0].WriteOnly {
		t.Errorf("FOO should not be write-only")
	}
	// Write-only state round-trips, and its sealed bytes are reapplied without
	// ever being decrypted.
	if !f.envApplied[1].WriteOnly {
		t.Errorf("SECRET should revert as write-only")
	}
	if string(f.envApplied[1].Value) != string(woSealed) {
		t.Errorf("write-only sealed bytes not round-tripped: %v", f.envApplied[1].Value)
	}
	if f.marked != "a1" {
		t.Errorf("entry not marked reverted: %q", f.marked)
	}
	if summary == "" {
		t.Error("empty summary")
	}
}

func TestRevertBaseDomain(t *testing.T) {
	f := &fakeStore{entry: store.AuditLog{
		ID:         "b1",
		Action:     "PUT /api/instance/base-domain",
		Revertable: true,
		Metadata:   meta(t, map[string]any{"base_domain": "old.example.com"}),
	}}
	bd := &fakeBaseDom{}
	rv := New(f, bd)
	if _, err := rv.Revert(context.Background(), "b1"); err != nil {
		t.Fatalf("revert: %v", err)
	}
	if !f.baseSet || f.baseDomain != "old.example.com" {
		t.Errorf("base domain not restored: set=%v val=%q", f.baseSet, f.baseDomain)
	}
	if bd.set != "old.example.com" {
		t.Errorf("live proxy not updated: %q", bd.set)
	}
}

func TestRevertApp(t *testing.T) {
	appID := "app-2"
	mem := 256
	before := map[string]any{"app": map[string]any{
		"name":         "Old Name",
		"git_url":      "https://example.com/r.git",
		"git_branch":   "main",
		"compose_file": "compose.yaml",
		"build_kind":   "auto",
		"build_config": json.RawMessage("{}"),
		"mem_limit_mb": mem,
	}}
	f := &fakeStore{entry: store.AuditLog{
		ID:         "c1",
		Action:     "PATCH /api/apps/{id}",
		TargetID:   &appID,
		Revertable: true,
		Metadata:   meta(t, before),
	}}
	rv := New(f, nil)
	if _, err := rv.Revert(context.Background(), "c1"); err != nil {
		t.Fatalf("revert: %v", err)
	}
	if !f.appPatch || f.appName == nil || *f.appName != "Old Name" {
		t.Errorf("app not patched with prior name: patch=%v name=%v", f.appPatch, f.appName)
	}
	if f.appMem == nil || *f.appMem != 256 {
		t.Errorf("mem limit not restored: %v", f.appMem)
	}
}

func TestRevertAppNilMemClears(t *testing.T) {
	appID := "app-3"
	before := map[string]any{"app": map[string]any{
		"name":         "X",
		"git_url":      "https://example.com/r.git",
		"git_branch":   "main",
		"compose_file": "compose.yaml",
		"build_kind":   "auto",
		"build_config": json.RawMessage("{}"),
		"mem_limit_mb": nil,
	}}
	f := &fakeStore{entry: store.AuditLog{
		ID: "c2", Action: "PATCH /api/apps/{id}", TargetID: &appID,
		Revertable: true, Metadata: meta(t, before),
	}}
	if _, err := New(f, nil).Revert(context.Background(), "c2"); err != nil {
		t.Fatalf("revert: %v", err)
	}
	// nil prior limit must translate to an active clear (0), not "unchanged".
	if f.appMem == nil || *f.appMem != 0 {
		t.Errorf("nil prior mem should clear to 0, got %v", f.appMem)
	}
}

func TestRevertNotRevertable(t *testing.T) {
	f := &fakeStore{entry: store.AuditLog{ID: "d1", Action: "POST /api/instance/reset", Revertable: false}}
	if _, err := New(f, nil).Revert(context.Background(), "d1"); !errors.Is(err, ErrNotRevertable) {
		t.Errorf("want ErrNotRevertable, got %v", err)
	}
}

func TestRevertUnknownAction(t *testing.T) {
	f := &fakeStore{entry: store.AuditLog{
		ID: "e1", Action: "DELETE /api/apps/{id}/domains/{domainId}",
		Revertable: true, Metadata: meta(t, map[string]any{"x": 1}),
	}}
	if _, err := New(f, nil).Revert(context.Background(), "e1"); !errors.Is(err, ErrNotRevertable) {
		t.Errorf("want ErrNotRevertable for unknown action, got %v", err)
	}
}

func TestRevertAlreadyReverted(t *testing.T) {
	now := time.Now()
	f := &fakeStore{entry: store.AuditLog{
		ID: "f1", Action: "PUT /api/instance/base-domain",
		Revertable: true, RevertedAt: &now,
		Metadata: meta(t, map[string]any{"base_domain": ""}),
	}}
	if _, err := New(f, nil).Revert(context.Background(), "f1"); !errors.Is(err, store.ErrConflict) {
		t.Errorf("want ErrConflict for already-reverted, got %v", err)
	}
}

func TestRevertMissingSnapshot(t *testing.T) {
	f := &fakeStore{entry: store.AuditLog{
		ID: "g1", Action: "PUT /api/instance/base-domain", Revertable: true,
	}}
	if _, err := New(f, nil).Revert(context.Background(), "g1"); !errors.Is(err, ErrNotRevertable) {
		t.Errorf("want ErrNotRevertable for missing snapshot, got %v", err)
	}
}

func TestRevertGetError(t *testing.T) {
	f := &fakeStore{getErr: store.ErrNotFound}
	if _, err := New(f, nil).Revert(context.Background(), "z"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	_ = strptr // keep helper referenced for future cases
}
