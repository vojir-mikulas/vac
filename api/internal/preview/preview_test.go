package preview

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

func TestDeriveSlug(t *testing.T) {
	tests := []struct {
		parent, branch, want string
	}{
		{"blog", "feature/login", "blog-feature-login"},
		{"blog", "Fix_Bug #42", "blog-fix-bug-42"},
		{"blog", "main", "blog-main"},
		{"blog", "---", ""},                // branch slugifies to nothing
		{"blog", "", ""},                   // empty branch
		{strings.Repeat("a", 63), "x", ""}, // no room for a branch segment
	}
	for _, tc := range tests {
		got := DeriveSlug(tc.parent, tc.branch)
		if got != tc.want {
			t.Errorf("DeriveSlug(%q,%q) = %q, want %q", tc.parent, tc.branch, got, tc.want)
		}
		if got != "" && len(got) > maxSlugLen {
			t.Errorf("DeriveSlug(%q,%q) = %q exceeds %d chars", tc.parent, tc.branch, got, maxSlugLen)
		}
	}
}

func TestDeriveSlugTruncates(t *testing.T) {
	parent := strings.Repeat("p", 30)
	branch := strings.Repeat("b", 100)
	got := DeriveSlug(parent, branch)
	if len(got) > maxSlugLen {
		t.Fatalf("slug %q is %d chars, want <= %d", got, len(got), maxSlugLen)
	}
	if !strings.HasPrefix(got, parent+"-") {
		t.Fatalf("slug %q lost its parent prefix", got)
	}
	if strings.HasSuffix(got, "-") {
		t.Fatalf("slug %q ends with a hyphen", got)
	}
}

// fakeStore implements the preview.Store interface with in-memory maps.
type fakeStore struct {
	apps        map[string]store.App
	previews    map[string]store.App // keyed by "parent|branch"
	previewCnt  int
	deployments []string // app ids a deployment was created for
	active      map[string]bool
	created     []store.App
	deleted     []string
}

func newFakeStore() *fakeStore {
	return &fakeStore{apps: map[string]store.App{}, previews: map[string]store.App{}, active: map[string]bool{}}
}

func (f *fakeStore) GetApp(_ context.Context, id string) (store.App, error) {
	a, ok := f.apps[id]
	if !ok {
		return store.App{}, store.ErrNotFound
	}
	return a, nil
}

func (f *fakeStore) CreatePreviewApp(_ context.Context, parent store.App, slug, branch string) (store.App, error) {
	id := "pv-" + slug
	a := store.App{ID: id, Slug: slug, GitBranch: branch, IsPreview: true, ParentAppID: &parent.ID}
	f.apps[id] = a
	f.previews[parent.ID+"|"+branch] = a
	f.previewCnt++
	f.created = append(f.created, a)
	return a, nil
}

func (f *fakeStore) GetPreviewByParentAndBranch(_ context.Context, parentID, branch string) (store.App, error) {
	a, ok := f.previews[parentID+"|"+branch]
	if !ok {
		return store.App{}, store.ErrNotFound
	}
	return a, nil
}

func (f *fakeStore) ListExpiredPreviews(_ context.Context, _ time.Duration) ([]store.App, error) {
	return nil, nil
}
func (f *fakeStore) CountPreviews(_ context.Context) (int, error)       { return f.previewCnt, nil }
func (f *fakeStore) TouchPreviewPush(_ context.Context, _ string) error { return nil }
func (f *fakeStore) HasActiveDeployment(_ context.Context, appID string) (bool, error) {
	return f.active[appID], nil
}
func (f *fakeStore) CreateDeployment(_ context.Context, appID, _ string, _ *string) (store.Deployment, error) {
	f.deployments = append(f.deployments, appID)
	return store.Deployment{ID: "d-" + appID, AppID: appID}, nil
}
func (f *fakeStore) ActiveDeploymentIDsForApp(_ context.Context, _ string) ([]string, error) {
	return nil, nil
}
func (f *fakeStore) DeleteApp(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	delete(f.apps, id)
	if a, ok := f.apps[id]; ok && a.IsPreview {
		f.previewCnt--
	}
	return nil
}

type fakeEnqueuer struct{ enqueued []string }

func (f *fakeEnqueuer) Enqueue(id string) error { f.enqueued = append(f.enqueued, id); return nil }

func TestEnsurePreviewCreatesAndEnqueues(t *testing.T) {
	fs := newFakeStore()
	fs.apps["parent"] = store.App{ID: "parent", Slug: "blog", GitBranch: "main"}
	enq := &fakeEnqueuer{}
	svc := New(fs, enq, nil, nil, nil, nil, Config{MaxPreviews: 5}, nil)

	if err := svc.EnsurePreview(context.Background(), "parent", "feature/x"); err != nil {
		t.Fatalf("EnsurePreview: %v", err)
	}
	if len(fs.created) != 1 || fs.created[0].Slug != "blog-feature-x" {
		t.Fatalf("expected one preview slug blog-feature-x, got %+v", fs.created)
	}
	if len(enq.enqueued) != 1 {
		t.Fatalf("expected one enqueue, got %v", enq.enqueued)
	}
}

func TestEnsurePreviewRedeploysExisting(t *testing.T) {
	fs := newFakeStore()
	fs.apps["parent"] = store.App{ID: "parent", Slug: "blog", GitBranch: "main"}
	enq := &fakeEnqueuer{}
	svc := New(fs, enq, nil, nil, nil, nil, Config{MaxPreviews: 5}, nil)

	_ = svc.EnsurePreview(context.Background(), "parent", "feat")
	createdAfterFirst := len(fs.created)
	if err := svc.EnsurePreview(context.Background(), "parent", "feat"); err != nil {
		t.Fatalf("second EnsurePreview: %v", err)
	}
	if len(fs.created) != createdAfterFirst {
		t.Fatalf("second push created a new preview app; want redeploy of the same one")
	}
	if len(enq.enqueued) != 2 {
		t.Fatalf("expected two enqueues (create + redeploy), got %v", enq.enqueued)
	}
}

func TestEnsurePreviewCoalescesActiveDeploy(t *testing.T) {
	fs := newFakeStore()
	fs.apps["parent"] = store.App{ID: "parent", Slug: "blog", GitBranch: "main"}
	enq := &fakeEnqueuer{}
	svc := New(fs, enq, nil, nil, nil, nil, Config{MaxPreviews: 5}, nil)

	_ = svc.EnsurePreview(context.Background(), "parent", "feat")
	// Mark the preview's deploy active; a second push should coalesce (no enqueue).
	fs.active["pv-blog-feat"] = true
	_ = svc.EnsurePreview(context.Background(), "parent", "feat")
	if len(enq.enqueued) != 1 {
		t.Fatalf("expected coalesce (1 enqueue), got %v", enq.enqueued)
	}
}

type capNotifier struct{ calls int }

func (c *capNotifier) PreviewCapReached(_, _, _ string, _ int) { c.calls++ }

func TestEnsurePreviewRefusesAtCap(t *testing.T) {
	fs := newFakeStore()
	fs.apps["parent"] = store.App{ID: "parent", Slug: "blog", GitBranch: "main"}
	fs.previewCnt = 5 // already at cap
	enq := &fakeEnqueuer{}
	notif := &capNotifier{}
	svc := New(fs, enq, nil, nil, nil, notif, Config{MaxPreviews: 5}, nil)

	err := svc.EnsurePreview(context.Background(), "parent", "feat")
	if !errors.Is(err, ErrCapReached) {
		t.Fatalf("want ErrCapReached, got %v", err)
	}
	if notif.calls != 1 {
		t.Fatalf("want one cap notification, got %d", notif.calls)
	}
	if len(fs.created) != 0 {
		t.Fatalf("a preview was created despite the cap: %+v", fs.created)
	}
}

func TestTeardownRefusesNonPreview(t *testing.T) {
	fs := newFakeStore()
	fs.apps["prod"] = store.App{ID: "prod", Slug: "blog", IsPreview: false}
	svc := New(fs, &fakeEnqueuer{}, nil, nil, nil, nil, Config{MaxPreviews: 5}, nil)

	if err := svc.Teardown(context.Background(), "prod"); !errors.Is(err, ErrNotPreview) {
		t.Fatalf("want ErrNotPreview, got %v", err)
	}
	if len(fs.deleted) != 0 {
		t.Fatalf("a non-preview app was deleted: %v", fs.deleted)
	}
}

func TestTeardownDeletesPreview(t *testing.T) {
	fs := newFakeStore()
	parentID := "parent"
	fs.apps["pv1"] = store.App{ID: "pv1", Slug: "blog-feat", IsPreview: true, ParentAppID: &parentID}
	svc := New(fs, &fakeEnqueuer{}, nil, nil, nil, nil, Config{MaxPreviews: 5}, nil)

	if err := svc.Teardown(context.Background(), "pv1"); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if len(fs.deleted) != 1 || fs.deleted[0] != "pv1" {
		t.Fatalf("want pv1 deleted, got %v", fs.deleted)
	}
}
