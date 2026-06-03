package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/vojir-mikulas/vac/api/internal/auditdiff"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// diffFakeStore satisfies auditdiff.Store for the handler-mapping tests.
type diffFakeStore struct {
	entry  store.AuditLog
	getErr error
}

func (f *diffFakeStore) GetAuditLog(_ context.Context, _ string) (store.AuditLog, error) {
	return f.entry, f.getErr
}
func (f *diffFakeStore) ListEnvVarsForApp(_ context.Context, _ string) ([]store.EnvVar, error) {
	return nil, nil
}
func (f *diffFakeStore) GetApp(_ context.Context, _ string) (store.App, error) {
	return store.App{}, nil
}
func (f *diffFakeStore) GetInstanceSettings(_ context.Context) (store.InstanceSettings, error) {
	return store.InstanceSettings{}, nil
}

func TestPreviewAuditStatusMapping(t *testing.T) {
	t.Parallel()
	baseDom := func(t *testing.T) json.RawMessage {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"before": map[string]any{"base_domain": "old.example.com"}})
		return raw
	}
	cases := []struct {
		name  string
		store *diffFakeStore
		want  int
	}{
		{
			name:  "200 for a curated entry",
			store: &diffFakeStore{entry: store.AuditLog{Action: "PUT /api/instance/base-domain", Metadata: baseDom(t)}},
			want:  http.StatusOK,
		},
		{
			name:  "404 when the entry is gone",
			store: &diffFakeStore{getErr: store.ErrNotFound},
			want:  http.StatusNotFound,
		},
		{
			name:  "422 for a non-diffable action",
			store: &diffFakeStore{entry: store.AuditLog{Action: "POST /api/apps/{id}/deployments"}},
			want:  http.StatusUnprocessableEntity,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := chi.NewRouter()
			r.Get("/audit/{id}/diff", PreviewAudit(auditdiff.New(c.store, nil)))
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/audit/abc/diff", nil))
			if rr.Code != c.want {
				t.Fatalf("status = %d; want %d (body %s)", rr.Code, c.want, rr.Body.String())
			}
		})
	}
}
