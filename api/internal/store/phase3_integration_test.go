//go:build integration

package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

func TestDomainsCRUD(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	a := testApp(t, s, "domains-app")

	// A domain FKs to a concrete service (app_id, service_name).
	cid := "c1"
	port := 3000
	if _, err := s.UpsertService(ctx, a.ID, "web", &cid, nil, &port, "running"); err != nil {
		t.Fatalf("UpsertService: %v", err)
	}

	d, err := s.CreateDomain(ctx, a.ID, "web", "blog.example.com", store.DomainTypeAuto)
	if err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}
	if d.Type != store.DomainTypeAuto || d.CertStatus != store.CertStatusPending {
		t.Errorf("defaults wrong: %+v", d)
	}

	got, err := s.GetDomainByHostname(ctx, "blog.example.com")
	if err != nil || got.ID != d.ID {
		t.Fatalf("GetDomainByHostname = %+v, %v", got, err)
	}

	// Global hostname uniqueness → ErrConflict.
	if _, err := s.CreateDomain(ctx, a.ID, "web", "blog.example.com", store.DomainTypeCustom); !errors.Is(err, store.ErrConflict) {
		t.Errorf("duplicate hostname err = %v, want ErrConflict", err)
	}

	list, err := s.ListDomainsByApp(ctx, a.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListDomainsByApp = %d, %v", len(list), err)
	}

	if err := s.SetCertStatus(ctx, d.ID, store.CertStatusActive); err != nil {
		t.Fatalf("SetCertStatus: %v", err)
	}
	got, _ = s.GetDomainByHostname(ctx, "blog.example.com")
	if got.CertStatus != store.CertStatusActive {
		t.Errorf("cert_status = %q", got.CertStatus)
	}

	if err := s.DeleteDomain(ctx, a.ID, d.ID); err != nil {
		t.Fatalf("DeleteDomain: %v", err)
	}
	if _, err := s.GetDomainByHostname(ctx, "blog.example.com"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("after delete err = %v, want ErrNotFound", err)
	}
}

func TestDomainsCascadeOnServiceDelete(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	a := testApp(t, s, "domains-cascade")
	port := 8080
	if _, err := s.UpsertService(ctx, a.ID, "web", nil, nil, &port, "running"); err != nil {
		t.Fatalf("UpsertService: %v", err)
	}
	if _, err := s.CreateDomain(ctx, a.ID, "web", "cascade.example.com", store.DomainTypeAuto); err != nil {
		t.Fatalf("CreateDomain: %v", err)
	}
	// Removing the service cascades to its domains (composite FK).
	if err := s.DeleteService(ctx, a.ID, "web"); err != nil {
		t.Fatalf("DeleteService: %v", err)
	}
	list, err := s.ListDomainsByApp(ctx, a.ID)
	if err != nil || len(list) != 0 {
		t.Errorf("domains after service delete = %d, %v; want 0", len(list), err)
	}
}

func TestRequestMetricsUpsertAndSeries(t *testing.T) {
	s := setup(t)
	ctx := context.Background()
	a := testApp(t, s, "metrics-app")

	bucket := time.Now().UTC().Truncate(10 * time.Second)
	rows := []store.RequestBucket{
		{AppID: a.ID, ServiceName: "web", BucketTS: bucket, Requests: 2, Errors: 1, BytesOut: 100},
	}
	if err := s.UpsertRequestBuckets(ctx, rows); err != nil {
		t.Fatalf("UpsertRequestBuckets: %v", err)
	}
	// Same bucket again → counters accumulate, not duplicate.
	if err := s.UpsertRequestBuckets(ctx, rows); err != nil {
		t.Fatalf("UpsertRequestBuckets 2: %v", err)
	}

	series, err := s.QueryRequestSeries(ctx, a.ID, "web", bucket.Add(-time.Minute))
	if err != nil {
		t.Fatalf("QueryRequestSeries: %v", err)
	}
	if len(series) != 1 {
		t.Fatalf("series len = %d, want 1 (%+v)", len(series), series)
	}
	if series[0].Requests != 4 || series[0].Errors != 2 || series[0].BytesOut != 200 {
		t.Errorf("accumulated wrong: %+v", series[0])
	}

	// App-level (service="") sums across services.
	if err := s.UpsertRequestBuckets(ctx, []store.RequestBucket{
		{AppID: a.ID, ServiceName: "api", BucketTS: bucket, Requests: 5},
	}); err != nil {
		t.Fatalf("UpsertRequestBuckets api: %v", err)
	}
	appSeries, err := s.QueryRequestSeries(ctx, a.ID, "", bucket.Add(-time.Minute))
	if err != nil {
		t.Fatalf("QueryRequestSeries app: %v", err)
	}
	if len(appSeries) != 1 || appSeries[0].Requests != 9 {
		t.Errorf("app series = %+v, want one point with 9 requests", appSeries)
	}

	// Prune everything older than now+1m removes the bucket.
	n, err := s.DeleteRequestMetricsOlderThan(ctx, time.Now().Add(time.Minute))
	if err != nil {
		t.Fatalf("DeleteRequestMetricsOlderThan: %v", err)
	}
	if n < 2 {
		t.Errorf("pruned %d rows, want >= 2", n)
	}
}
