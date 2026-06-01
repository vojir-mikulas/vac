// Package audit carries a per-request, mutable audit record through the request
// context. The central audit middleware (api/internal/server/middleware) seeds
// one record per mutating request and persists it once the handler returns;
// handlers enrich it in passing with a one-line hook:
//
//	audit.Describe(r.Context(), "renamed app to "+name)
//	audit.SetTarget(r.Context(), "app", id)
//
// The record is a pointer shared between middleware and handler, so a handler's
// mutations are visible to the middleware after ServeHTTP — no return plumbing.
// All helpers no-op when no record is present (e.g. a GET, or a handler invoked
// outside the audited /api group), so callers never need a nil check.
package audit

import "context"

type ctxKey int

const recordKey ctxKey = iota

// Record is the mutable, handler-enrichable part of an audit entry. The
// middleware fills in the non-discretionary fields (actor, route, ip, outcome)
// itself; everything here is the handler's optional contribution.
type Record struct {
	// Summary is a short human sentence ("deleted app blog").
	Summary string
	// TargetType / TargetID identify the primary object acted on ("app", id).
	TargetType string
	TargetID   string
	// Metadata is an optional structured payload (e.g. a before-snapshot for
	// the curated-revert work in plan 11). Marshaled to JSONB by the store.
	Metadata map[string]any
	// Revertable marks this action as one of the curated, safely-invertible set
	// (plan 11, Part 2). Set via Snapshot, which also stows the before-state.
	Revertable bool
	// Skip, when set, tells the middleware not to persist this entry — for the
	// rare mutating route that is pure noise (health pokes, idempotent probes).
	Skip bool
}

// BeforeKey is the reserved Metadata key under which Snapshot stores the
// pre-mutation state. The revert engine reads it back to compute the inverse.
const BeforeKey = "before"

// NewRecord returns an empty record for the middleware to seed the context with.
func NewRecord() *Record { return &Record{} }

// WithRecord attaches rec to ctx.
func WithRecord(ctx context.Context, rec *Record) context.Context {
	return context.WithValue(ctx, recordKey, rec)
}

// FromContext returns the request's audit record, or nil if none is attached.
func FromContext(ctx context.Context) *Record {
	rec, _ := ctx.Value(recordKey).(*Record)
	return rec
}

// Describe sets the human summary line. No-op if no record is attached.
func Describe(ctx context.Context, summary string) {
	if rec := FromContext(ctx); rec != nil {
		rec.Summary = summary
	}
}

// SetTarget records the primary object the action touched. No-op if no record.
func SetTarget(ctx context.Context, targetType, targetID string) {
	if rec := FromContext(ctx); rec != nil {
		rec.TargetType = targetType
		rec.TargetID = targetID
	}
}

// SetMetadata attaches a structured payload (snapshots, diffs). No-op if no
// record. Replaces any previously-set metadata.
func SetMetadata(ctx context.Context, m map[string]any) {
	if rec := FromContext(ctx); rec != nil {
		rec.Metadata = m
	}
}

// Skip marks the current request as not worth persisting. No-op if no record.
func Skip(ctx context.Context) {
	if rec := FromContext(ctx); rec != nil {
		rec.Skip = true
	}
}

// Snapshot marks the action revertable and stores the pre-mutation state under
// the reserved BeforeKey so the revert engine can reapply it. Call it BEFORE
// mutating, with the prior state. Secrets must already be in their sealed form
// here — the snapshot lands in the audit_log JSONB, which is not encrypted.
// No-op if no record (e.g. invoked outside the audited /api group).
func Snapshot(ctx context.Context, before map[string]any) {
	rec := FromContext(ctx)
	if rec == nil {
		return
	}
	if rec.Metadata == nil {
		rec.Metadata = map[string]any{}
	}
	rec.Metadata[BeforeKey] = before
	rec.Revertable = true
}
