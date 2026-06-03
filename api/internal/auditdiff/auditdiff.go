// Package auditdiff computes a sanitized before→current diff for a curated audit
// entry (plan 22). It is the safe, opt-in way to surface the before-snapshot the
// activity DTO deliberately hides: the engine reads the raw snapshot, fetches
// current DB state, and returns a normalized row model with secrets stripped.
//
// Security: env-var snapshots hold sealed ciphertext (base64), never plaintext,
// and the audit JSONB is not decrypted. The diff keeps it that way — sensitive
// and write-only keys are masked and only compared as sealed bytes; only
// non-sensitive values are ever decrypted (the same rule as handler.ListAppEnv).
// No sealed/base64 ciphertext ever reaches the caller.
package auditdiff

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/vojir-mikulas/vac/api/internal/crypto"
	"github.com/vojir-mikulas/vac/api/internal/revert"
	"github.com/vojir-mikulas/vac/api/internal/store"
)

// ErrNotDiffable is returned for an audit entry outside the curated set (or one
// whose before-snapshot is missing/corrupt). Callers map it to HTTP 422,
// mirroring revert.ErrNotRevertable.
var ErrNotDiffable = errors.New("auditdiff: no preview available for this action")

// FieldStatus is the per-row change classification.
type FieldStatus string

const (
	StatusAdded     FieldStatus = "added"
	StatusRemoved   FieldStatus = "removed"
	StatusChanged   FieldStatus = "changed"
	StatusUnchanged FieldStatus = "unchanged" // env keys present + equal in both
)

// Row is one normalized before/after row, shared across all action kinds.
type Row struct {
	Label  string      `json:"label"` // env key, app field name, or "Base domain"
	Status FieldStatus `json:"status"`
	Before *string     `json:"before,omitempty"` // nil when not applicable / masked
	After  *string     `json:"after,omitempty"`
	Masked bool        `json:"masked"` // true => value hidden (sensitive/write_only)
}

// Diff is the endpoint payload.
type Diff struct {
	Kind        string    `json:"kind"` // "env" | "app" | "base_domain"
	Rows        []Row     `json:"rows"`
	CurrentAsOf time.Time `json:"current_as_of"` // when "after" was read
	// ChangedSince is true if the audited entry was followed by a later change to
	// the same target, so "current" may not equal the immediate after of THIS
	// action. False in the MVP (the UI labels "after" as current state).
	ChangedSince bool `json:"changed_since"`
}

// Store is the persistence slice the builder needs. *store.Store satisfies it.
type Store interface {
	GetAuditLog(ctx context.Context, id string) (store.AuditLog, error)
	ListEnvVarsForApp(ctx context.Context, appID string) ([]store.EnvVar, error)
	GetApp(ctx context.Context, id string) (store.App, error)
	GetInstanceSettings(ctx context.Context) (store.InstanceSettings, error)
}

// Builder computes diffs. box may be nil (no master key) — every env value then
// degrades to masked rather than failing.
type Builder struct {
	store Store
	box   *crypto.Box
}

// New wires a Builder.
func New(s Store, box *crypto.Box) *Builder {
	return &Builder{store: s, box: box}
}

// metaShape mirrors the audit_log metadata JSON: the before-snapshot lives under
// audit.BeforeKey ("before"). Same shape the reverter unmarshals.
type metaShape struct {
	Before json.RawMessage `json:"before"`
}

// Compute resolves the entry, validates it carries a before-snapshot, fetches
// current state, and returns the normalized + sanitized diff. store.ErrNotFound
// passes through (→ 404); non-curated/snapshotless entries return ErrNotDiffable
// (→ 422). CurrentAsOf is stamped by the caller-visible clock.
func (b *Builder) Compute(ctx context.Context, id string) (Diff, error) {
	entry, err := b.store.GetAuditLog(ctx, id)
	if err != nil {
		return Diff{}, err
	}

	var meta metaShape
	if len(entry.Metadata) > 0 {
		if err := json.Unmarshal(entry.Metadata, &meta); err != nil {
			return Diff{}, fmt.Errorf("%w: metadata: %v", ErrNotDiffable, err)
		}
	}
	if len(meta.Before) == 0 {
		return Diff{}, fmt.Errorf("%w: no before-snapshot", ErrNotDiffable)
	}

	var diff Diff
	switch {
	case revert.ActionMatches(entry.Action, "PUT", "/apps/{id}/env"):
		diff, err = b.diffEnv(ctx, revert.TargetID(entry), meta.Before)
	case revert.ActionMatches(entry.Action, "PATCH", "/apps/{id}"):
		diff, err = b.diffApp(ctx, revert.TargetID(entry), meta.Before)
	case revert.ActionMatches(entry.Action, "PUT", "/instance/base-domain"):
		diff, err = b.diffBaseDomain(ctx, meta.Before)
	default:
		return Diff{}, fmt.Errorf("%w: %s", ErrNotDiffable, entry.Action)
	}
	if err != nil {
		return Diff{}, err
	}
	// "after" was just read from the DB; stamp the as-of clock. ChangedSince stays
	// false in the MVP — the UI labels "after" honestly as current state.
	diff.CurrentAsOf = b.now()
	return diff, nil
}

// now returns the current wall-clock time; isolated so tests could stub it if
// needed (kept as time.Now today).
func (b *Builder) now() time.Time { return time.Now() }

// diffEnv is the security-critical path. It compares the prior env set (sealed
// bytes from the snapshot) against the current set, revealing only non-sensitive
// values and masking everything else. It NEVER decrypts a sensitive or
// write-only key; changed-detection for those compares sealed bytes, an
// inequality that is a safe proxy for "the value changed" without decryption.
//
// Edge: re-sealing identical plaintext yields different ciphertext under the
// nonce'd box, so a sensitive key may read as "changed" when the plaintext was
// in fact identical. Acceptable — we never claim WHAT changed for masked keys,
// only that it did.
func (b *Builder) diffEnv(ctx context.Context, appID string, before json.RawMessage) (Diff, error) {
	if appID == "" {
		return Diff{}, fmt.Errorf("%w: env snapshot has no app target", ErrNotDiffable)
	}
	var snap revert.EnvSnapshot
	if err := json.Unmarshal(before, &snap); err != nil {
		return Diff{}, fmt.Errorf("%w: env snapshot: %v", ErrNotDiffable, err)
	}

	beforeByKey := map[string]envSide{}
	for _, e := range snap.Env {
		sealed, err := base64.StdEncoding.DecodeString(e.Value)
		if err != nil {
			return Diff{}, fmt.Errorf("%w: env value decode: %v", ErrNotDiffable, err)
		}
		beforeByKey[e.Key] = envSide{sealed: sealed, sensitive: e.Sensitive, writeOnly: e.WriteOnly}
	}

	current, err := b.store.ListEnvVarsForApp(ctx, appID)
	if err != nil {
		return Diff{}, err
	}
	currentByKey := map[string]envSide{}
	for _, v := range current {
		currentByKey[v.Key] = envSide{sealed: v.Value, sensitive: v.Sensitive, writeOnly: v.WriteOnly}
	}

	keys := unionKeys(beforeByKey, currentByKey)
	rows := make([]Row, 0, len(keys))
	for _, k := range keys {
		bef, hasBefore := beforeByKey[k]
		cur, hasCurrent := currentByKey[k]

		// A key is revealable only when non-sensitive & non-write-only on the
		// side(s) it appears, and the box is available. Otherwise it is masked.
		revealable := b.box != nil
		if hasBefore && (bef.sensitive || bef.writeOnly) {
			revealable = false
		}
		if hasCurrent && (cur.sensitive || cur.writeOnly) {
			revealable = false
		}

		row := Row{Label: k, Masked: !revealable}
		switch {
		case hasBefore && !hasCurrent:
			row.Status = StatusRemoved
			if revealable {
				if p, ok := b.reveal(bef.sealed); ok {
					row.Before = &p
				} else {
					row.Masked = true
				}
			}
		case !hasBefore && hasCurrent:
			row.Status = StatusAdded
			if revealable {
				if p, ok := b.reveal(cur.sealed); ok {
					row.After = &p
				} else {
					row.Masked = true
				}
			}
		default: // present in both
			// A key whose sensitivity flipped between snapshots is treated as
			// changed and masked — its before/after can't be compared safely.
			sensitivityFlipped := bef.sensitive != cur.sensitive || bef.writeOnly != cur.writeOnly
			if revealable && !sensitivityFlipped {
				bp, bok := b.reveal(bef.sealed)
				cp, cok := b.reveal(cur.sealed)
				if bok && cok {
					row.Before, row.After = &bp, &cp
					if bp == cp {
						row.Status = StatusUnchanged
					} else {
						row.Status = StatusChanged
					}
				} else {
					// Decrypt failed unexpectedly — fall back to masked + sealed compare.
					row.Masked = true
					row.Status = sealedStatus(bef.sealed, cur.sealed)
				}
			} else {
				row.Masked = true
				if sensitivityFlipped {
					row.Status = StatusChanged
				} else {
					row.Status = sealedStatus(bef.sealed, cur.sealed)
				}
			}
		}
		rows = append(rows, row)
	}

	sortEnvRows(rows)
	return Diff{Kind: "env", Rows: rows}, nil
}

// reveal decrypts a sealed value, returning (plaintext, true) on success. A
// decrypt failure returns ("", false) so the caller can fall back to masking.
func (b *Builder) reveal(sealed []byte) (string, bool) {
	if b.box == nil {
		return "", false
	}
	plain, err := b.box.Open(sealed)
	if err != nil {
		return "", false
	}
	return string(plain), true
}

// sealedStatus classifies a both-present key by comparing sealed bytes only.
func sealedStatus(before, current []byte) FieldStatus {
	if bytes.Equal(before, current) {
		return StatusUnchanged
	}
	return StatusChanged
}

// diffApp compares the prior app config against current, emitting only the
// fields that changed. App config holds no secrets, so nothing is masked.
func (b *Builder) diffApp(ctx context.Context, appID string, before json.RawMessage) (Diff, error) {
	if appID == "" {
		return Diff{}, fmt.Errorf("%w: app snapshot has no app target", ErrNotDiffable)
	}
	var snap revert.AppSnapshot
	if err := json.Unmarshal(before, &snap); err != nil {
		return Diff{}, fmt.Errorf("%w: app snapshot: %v", ErrNotDiffable, err)
	}
	cur, err := b.store.GetApp(ctx, appID)
	if err != nil {
		return Diff{}, err
	}
	a := snap.App

	// Field order is stable and human-readable.
	fields := []struct {
		label         string
		before, after string
	}{
		{"Name", deref(a.Name), cur.Name},
		{"Git URL", deref(a.GitURL), cur.GitURL},
		{"Branch", deref(a.GitBranch), cur.GitBranch},
		{"Compose file", deref(a.ComposeFile), cur.ComposeFile},
		{"Build kind", deref(a.BuildKind), cur.BuildKind},
		{"Build config", normalizeJSON(a.BuildConfig), normalizeJSON(cur.BuildConfig)},
		{"Memory limit", memLimit(a.MemLimitMB), memLimit(cur.MemLimitMB)},
	}
	rows := make([]Row, 0, len(fields))
	for _, f := range fields {
		if f.before == f.after {
			continue
		}
		bef, aft := f.before, f.after
		rows = append(rows, Row{Label: f.label, Status: StatusChanged, Before: &bef, After: &aft})
	}
	return Diff{Kind: "app", Rows: rows}, nil
}

// diffBaseDomain compares the prior base domain against current as a single row.
func (b *Builder) diffBaseDomain(ctx context.Context, before json.RawMessage) (Diff, error) {
	var snap revert.BaseDomainSnapshot
	if err := json.Unmarshal(before, &snap); err != nil {
		return Diff{}, fmt.Errorf("%w: base-domain snapshot: %v", ErrNotDiffable, err)
	}
	settings, err := b.store.GetInstanceSettings(ctx)
	if err != nil {
		return Diff{}, err
	}
	bef := baseDomainStr(snap.BaseDomain)
	aft := baseDomainStr(settings.BaseDomain)
	row := Row{Label: "Base domain", Status: StatusChanged, Before: &bef, After: &aft}
	if bef == aft {
		row.Status = StatusUnchanged
	}
	return Diff{Kind: "base_domain", Rows: []Row{row}}, nil
}

// --- helpers ---

// envSide is one key's state on a single side of the diff (before or current).
type envSide struct {
	sealed    []byte
	sensitive bool
	writeOnly bool
}

func unionKeys(a, b map[string]envSide) []string {
	set := map[string]struct{}{}
	for k := range a {
		set[k] = struct{}{}
	}
	for k := range b {
		set[k] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	return out
}

// sortEnvRows orders changed/added/removed before unchanged, alpha within group.
func sortEnvRows(rows []Row) {
	rank := func(s FieldStatus) int {
		if s == StatusUnchanged {
			return 1
		}
		return 0
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if ri, rj := rank(rows[i].Status), rank(rows[j].Status); ri != rj {
			return ri < rj
		}
		return rows[i].Label < rows[j].Label
	})
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// memLimit stringifies a per-app memory ceiling; nil/0 means unlimited.
func memLimit(mb *int) string {
	if mb == nil || *mb == 0 {
		return "unlimited"
	}
	return strconv.Itoa(*mb) + " MB"
}

// normalizeJSON compacts a JSON blob so semantically-equal configs (differing
// only in whitespace) compare equal; empty is rendered as "{}".
func normalizeJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}

// baseDomainStr renders an empty override as the explicit "(cleared)" marker.
func baseDomainStr(s string) string {
	if s == "" {
		return "(cleared)"
	}
	return s
}
