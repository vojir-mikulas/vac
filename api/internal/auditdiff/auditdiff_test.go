package auditdiff

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// fakeStore is an in-memory Store for exercising the diff builder without Postgres.
type fakeStore struct {
	entry    store.AuditLog
	getErr   error
	env      []store.EnvVar
	app      store.App
	settings store.InstanceSettings
}

func (f *fakeStore) GetAuditLog(_ context.Context, _ string) (store.AuditLog, error) {
	return f.entry, f.getErr
}

func (f *fakeStore) ListEnvVarsForApp(_ context.Context, _ string) ([]store.EnvVar, error) {
	return f.env, nil
}
func (f *fakeStore) GetApp(_ context.Context, _ string) (store.App, error) { return f.app, nil }
func (f *fakeStore) GetInstanceSettings(_ context.Context) (store.InstanceSettings, error) {
	return f.settings, nil
}

func newBox(t *testing.T) *crypto.Box {
	t.Helper()
	key := make([]byte, crypto.KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	b, err := crypto.New(key)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func meta(t *testing.T, before map[string]any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"before": before})
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	return raw
}

func seal(t *testing.T, box *crypto.Box, plain string) []byte {
	t.Helper()
	sealed, err := box.Seal([]byte(plain))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	return sealed
}

func appID() *string { s := "app-1"; return &s }

func rowByLabel(rows []Row, label string) (Row, bool) {
	for _, r := range rows {
		if r.Label == label {
			return r, true
		}
	}
	return Row{}, false
}

// envBefore builds an env before-snapshot blob from sealed entries.
func envBefore(entries ...store.EnvVar) map[string]any {
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]any{
			"key":        e.Key,
			"value":      base64.StdEncoding.EncodeToString(e.Value),
			"sensitive":  e.Sensitive,
			"write_only": e.WriteOnly,
		})
	}
	return map[string]any{"env": out}
}

func TestDiffEnvClassification(t *testing.T) {
	box := newBox(t)
	// before: KEEP=1 (nonsensitive), CHANGE=old, GONE=x, SECRET=sealed(sensitive)
	keepSealed := seal(t, box, "1")
	changeOld := seal(t, box, "old")
	goneSealed := seal(t, box, "x")
	secretOld := seal(t, box, "topsecret")
	before := envBefore(
		store.EnvVar{Key: "KEEP", Value: keepSealed},
		store.EnvVar{Key: "CHANGE", Value: changeOld},
		store.EnvVar{Key: "GONE", Value: goneSealed},
		store.EnvVar{Key: "SECRET", Value: secretOld, Sensitive: true},
	)
	// current: KEEP=1 (unchanged), CHANGE=new, NEW=added, SECRET=changed sealed
	current := []store.EnvVar{
		{Key: "CHANGE", Value: seal(t, box, "new")},
		{Key: "KEEP", Value: keepSealed},
		{Key: "NEW", Value: seal(t, box, "added")},
		{Key: "SECRET", Value: seal(t, box, "rotated"), Sensitive: true},
	}
	f := &fakeStore{
		entry: store.AuditLog{Action: "PUT /api/apps/{id}/env", TargetID: appID(), Metadata: meta(t, before)},
		env:   current,
	}
	diff, err := New(f, box).Compute(context.Background(), "x")
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if diff.Kind != "env" {
		t.Fatalf("kind = %q, want env", diff.Kind)
	}

	keep, _ := rowByLabel(diff.Rows, "KEEP")
	if keep.Status != StatusUnchanged || keep.After == nil || *keep.After != "1" {
		t.Errorf("KEEP: %+v", keep)
	}
	change, _ := rowByLabel(diff.Rows, "CHANGE")
	if change.Status != StatusChanged || change.Before == nil || *change.Before != "old" || change.After == nil || *change.After != "new" {
		t.Errorf("CHANGE: %+v", change)
	}
	gone, _ := rowByLabel(diff.Rows, "GONE")
	if gone.Status != StatusRemoved || gone.Before == nil || *gone.Before != "x" {
		t.Errorf("GONE: %+v", gone)
	}
	added, _ := rowByLabel(diff.Rows, "NEW")
	if added.Status != StatusAdded || added.After == nil || *added.After != "added" {
		t.Errorf("NEW: %+v", added)
	}

	// SECRET: sensitive both sides → masked, changed (sealed bytes differ), and
	// neither plaintext shipped.
	secret, _ := rowByLabel(diff.Rows, "SECRET")
	if !secret.Masked {
		t.Errorf("SECRET should be masked: %+v", secret)
	}
	if secret.Status != StatusChanged {
		t.Errorf("SECRET status = %q, want changed", secret.Status)
	}
	if secret.Before != nil || secret.After != nil {
		t.Errorf("SECRET must not carry values: %+v", secret)
	}
	// Defence-in-depth: no plaintext secret anywhere in the marshalled payload.
	blob, _ := json.Marshal(diff)
	for _, leak := range []string{"topsecret", "rotated", base64.StdEncoding.EncodeToString(secretOld)} {
		if strings.Contains(string(blob), leak) {
			t.Fatalf("payload leaked %q: %s", leak, blob)
		}
	}

	// Sort: unchanged (KEEP) comes after the changed/added/removed group.
	if diff.Rows[len(diff.Rows)-1].Label != "KEEP" {
		t.Errorf("unchanged row should sort last, got order %v", labels(diff.Rows))
	}
}

func labels(rows []Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Label
	}
	return out
}

func TestDiffEnvWriteOnlyMasked(t *testing.T) {
	box := newBox(t)
	wo := seal(t, box, "secret")
	before := envBefore(store.EnvVar{Key: "TOKEN", Value: wo, Sensitive: true, WriteOnly: true})
	current := []store.EnvVar{{Key: "TOKEN", Value: seal(t, box, "secret2"), Sensitive: true, WriteOnly: true}}
	f := &fakeStore{
		entry: store.AuditLog{Action: "PUT /api/apps/{id}/env", TargetID: appID(), Metadata: meta(t, before)},
		env:   current,
	}
	diff, err := New(f, box).Compute(context.Background(), "x")
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	tok, _ := rowByLabel(diff.Rows, "TOKEN")
	if !tok.Masked || tok.Before != nil || tok.After != nil {
		t.Errorf("write-only TOKEN must be masked with no values: %+v", tok)
	}
}

// A sensitive key whose plaintext is identical but re-sealed reads as changed —
// documented edge of the sealed-bytes proxy.
func TestDiffEnvSensitiveResealReadsChanged(t *testing.T) {
	box := newBox(t)
	before := envBefore(store.EnvVar{Key: "API_KEY", Value: seal(t, box, "same"), Sensitive: true})
	current := []store.EnvVar{{Key: "API_KEY", Value: seal(t, box, "same"), Sensitive: true}}
	f := &fakeStore{
		entry: store.AuditLog{Action: "PUT /api/apps/{id}/env", TargetID: appID(), Metadata: meta(t, before)},
		env:   current,
	}
	diff, err := New(f, box).Compute(context.Background(), "x")
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	k, _ := rowByLabel(diff.Rows, "API_KEY")
	if k.Status != StatusChanged {
		t.Errorf("re-sealed sensitive key should read changed, got %q", k.Status)
	}
}

func TestDiffEnvNilBoxMasksEverything(t *testing.T) {
	box := newBox(t)
	before := envBefore(store.EnvVar{Key: "PLAIN", Value: seal(t, box, "v")})
	current := []store.EnvVar{{Key: "PLAIN", Value: seal(t, box, "v2")}}
	f := &fakeStore{
		entry: store.AuditLog{Action: "PUT /api/apps/{id}/env", TargetID: appID(), Metadata: meta(t, before)},
		env:   current,
	}
	// nil box: even a non-sensitive key degrades to masked rather than failing.
	diff, err := New(f, nil).Compute(context.Background(), "x")
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	p, _ := rowByLabel(diff.Rows, "PLAIN")
	if !p.Masked || p.Before != nil || p.After != nil {
		t.Errorf("nil box should mask PLAIN: %+v", p)
	}
}

func TestDiffApp(t *testing.T) {
	before := map[string]any{"app": map[string]any{
		"name":         "Old",
		"git_url":      "https://example.com/r.git",
		"git_branch":   "main",
		"compose_file": "compose.yaml",
		"build_kind":   "auto",
		"build_config": json.RawMessage(`{"a":1}`),
		"mem_limit_mb": 256,
	}}
	f := &fakeStore{
		entry: store.AuditLog{Action: "PATCH /api/apps/{id}", TargetID: appID(), Metadata: meta(t, before)},
		app: store.App{
			Name: "New", GitURL: "https://example.com/r.git", GitBranch: "main",
			ComposeFile: "compose.yaml", BuildKind: "auto",
			BuildConfig: json.RawMessage(`{"a":1}`), MemLimitMB: nil,
		},
	}
	diff, err := New(f, nil).Compute(context.Background(), "x")
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if diff.Kind != "app" {
		t.Fatalf("kind = %q", diff.Kind)
	}
	// Only Name and Memory limit changed; equal fields (incl. build_config) skipped.
	if len(diff.Rows) != 2 {
		t.Fatalf("want 2 changed rows, got %d: %v", len(diff.Rows), labels(diff.Rows))
	}
	name, ok := rowByLabel(diff.Rows, "Name")
	if !ok || *name.Before != "Old" || *name.After != "New" {
		t.Errorf("Name row: %+v", name)
	}
	mem, ok := rowByLabel(diff.Rows, "Memory limit")
	if !ok || *mem.Before != "256 MB" || *mem.After != "unlimited" {
		t.Errorf("Memory limit row: %+v", mem)
	}
}

func TestDiffBaseDomain(t *testing.T) {
	before := map[string]any{"base_domain": "old.example.com"}
	f := &fakeStore{
		entry:    store.AuditLog{Action: "PUT /api/instance/base-domain", Metadata: meta(t, before)},
		settings: store.InstanceSettings{BaseDomain: ""},
	}
	diff, err := New(f, nil).Compute(context.Background(), "x")
	if err != nil {
		t.Fatalf("compute: %v", err)
	}
	if diff.Kind != "base_domain" || len(diff.Rows) != 1 {
		t.Fatalf("unexpected diff: %+v", diff)
	}
	r := diff.Rows[0]
	if r.Status != StatusChanged || *r.Before != "old.example.com" || *r.After != "(cleared)" {
		t.Errorf("base domain row: %+v", r)
	}
}

func TestComputeNotDiffable(t *testing.T) {
	cases := []struct {
		name  string
		entry store.AuditLog
	}{
		{"non-curated action", store.AuditLog{Action: "POST /api/apps/{id}/deployments", Metadata: meta(t, map[string]any{"x": 1})}},
		{"missing before", store.AuditLog{Action: "PUT /api/instance/base-domain"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeStore{entry: c.entry}
			if _, err := New(f, nil).Compute(context.Background(), "x"); !errors.Is(err, ErrNotDiffable) {
				t.Errorf("want ErrNotDiffable, got %v", err)
			}
		})
	}
}

func TestComputeNotFound(t *testing.T) {
	f := &fakeStore{getErr: store.ErrNotFound}
	if _, err := New(f, nil).Compute(context.Background(), "x"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}
