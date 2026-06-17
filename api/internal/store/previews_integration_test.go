//go:build integration

package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// TestPreviewLifecycle exercises the preview store surface end-to-end against a
// real Postgres: the migration columns, the non-sensitive env copy, the
// parent+branch lookup, the count/expiry queries, and the parent→preview
// ON DELETE CASCADE.
func TestPreviewLifecycle(t *testing.T) {
	s := setup(t)
	ctx := context.Background()

	parent, err := s.CreateApp(ctx, "Blog", "blog", "git@example.com:me/blog.git", "main", "compose.yaml", "compose", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	if parent.IsPreview {
		t.Fatalf("a normal app should not be a preview")
	}

	// One public env var and one secret — only the public one must be copied.
	if err := s.ReplaceEnvVars(ctx, parent.ID, []store.EnvVarInput{
		{Key: "PUBLIC_FLAG", Value: []byte("sealed-public"), Sensitive: false},
		{Key: "DATABASE_URL", Value: []byte("sealed-secret"), Sensitive: true},
	}); err != nil {
		t.Fatalf("ReplaceEnvVars: %v", err)
	}

	pv, err := s.CreatePreviewApp(ctx, parent, "blog-feat-x", "feat/x")
	if err != nil {
		t.Fatalf("CreatePreviewApp: %v", err)
	}
	if !pv.IsPreview || pv.ParentAppID == nil || *pv.ParentAppID != parent.ID {
		t.Fatalf("preview row not linked to parent: %+v", pv)
	}
	if pv.GitBranch != "feat/x" || pv.GitURL != parent.GitURL {
		t.Fatalf("preview did not inherit repo/branch: %+v", pv)
	}
	if pv.LastPreviewPushAt == nil {
		t.Fatalf("CreatePreviewApp should stamp last_preview_push_at")
	}

	// Only the non-sensitive env var should have been copied.
	envs, err := s.ListEnvVarsForApp(ctx, pv.ID)
	if err != nil {
		t.Fatalf("ListEnvVarsForApp: %v", err)
	}
	if len(envs) != 1 || envs[0].Key != "PUBLIC_FLAG" || envs[0].Sensitive {
		t.Fatalf("expected only the non-sensitive PUBLIC_FLAG copied, got %+v", envs)
	}

	// Lookup by parent+branch.
	got, err := s.GetPreviewByParentAndBranch(ctx, parent.ID, "feat/x")
	if err != nil || got.ID != pv.ID {
		t.Fatalf("GetPreviewByParentAndBranch = (%+v, %v), want %s", got, err, pv.ID)
	}
	if _, err := s.GetPreviewByParentAndBranch(ctx, parent.ID, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing branch should be ErrNotFound, got %v", err)
	}

	if n, err := s.CountPreviews(ctx); err != nil || n != 1 {
		t.Fatalf("CountPreviews = (%d, %v), want 1", n, err)
	}
	list, err := s.ListPreviewsForApp(ctx, parent.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListPreviewsForApp = (%d rows, %v), want 1", len(list), err)
	}

	// TTL: not expired with a long window, expired with a zero window.
	if exp, err := s.ListExpiredPreviews(ctx, time.Hour); err != nil || len(exp) != 0 {
		t.Fatalf("ListExpiredPreviews(1h) = (%d, %v), want 0", len(exp), err)
	}
	if exp, err := s.ListExpiredPreviews(ctx, -time.Second); err != nil || len(exp) != 1 {
		t.Fatalf("ListExpiredPreviews(past) = (%d, %v), want 1", len(exp), err)
	}

	// Deleting the parent cascades to the preview (parent_app_id ON DELETE CASCADE).
	if err := s.DeleteApp(ctx, parent.ID); err != nil {
		t.Fatalf("DeleteApp(parent): %v", err)
	}
	if _, err := s.GetApp(ctx, pv.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("preview should be cascade-deleted with its parent, got %v", err)
	}
	if n, err := s.CountPreviews(ctx); err != nil || n != 0 {
		t.Fatalf("CountPreviews after parent delete = (%d, %v), want 0", n, err)
	}
}
