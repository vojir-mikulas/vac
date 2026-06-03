// Package revert implements curated, one-click undo for the safely-invertible
// subset of audited actions (plan 11, Part 2). It is deliberately NOT universal
// undo: each revertable action carries a before-snapshot in its audit_log row
// (see package audit's Snapshot), and the engine reapplies that snapshot.
//
// The curated set today: env-var replace, instance base-domain, and app-config
// update. Deploy revert is plan 02's rollback (a separate, redeploy-based path);
// destructive actions (instance reset, hard app delete) are never revertable and
// the handler simply doesn't snapshot them.
package revert

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/vojir-mikulas/vac/api/internal/store"
)

// ErrNotRevertable is returned for an audit entry outside the curated set (or one
// whose snapshot is missing/corrupt). Callers map it to HTTP 422.
var ErrNotRevertable = errors.New("revert: action is not revertable")

// Store is the persistence slice the reverter needs. *store.Store satisfies it.
type Store interface {
	GetAuditLog(ctx context.Context, id string) (store.AuditLog, error)
	MarkAuditReverted(ctx context.Context, id string) error
	ReplaceEnvVars(ctx context.Context, appID string, vars []store.EnvVarInput) error
	GetApp(ctx context.Context, id string) (store.App, error)
	UpdateApp(ctx context.Context, id string, name, gitURL, gitBranch, composeFile, buildKind *string, buildConfig json.RawMessage, memLimitMB *int) (store.App, error)
	SetBaseDomain(ctx context.Context, baseDomain string) error
}

// BaseDomainSetter applies a base-domain change to the live proxy so a reverted
// base domain takes effect immediately, mirroring the forward handler. Optional
// (nil in tests / when the proxy is not wired).
type BaseDomainSetter interface {
	SetBaseDomain(string)
}

// Reverter applies the inverse of a revertable audit entry.
type Reverter struct {
	store   Store
	baseDom BaseDomainSetter
}

// New wires a Reverter. baseDom may be nil.
func New(s Store, baseDom BaseDomainSetter) *Reverter {
	return &Reverter{store: s, baseDom: baseDom}
}

// metaShape mirrors the audit_log metadata JSON: the before-snapshot lives under
// audit.BeforeKey ("before").
type metaShape struct {
	Before json.RawMessage `json:"before"`
}

// Revert undoes the audit entry with the given id and returns a human summary of
// what it did (used as the summary of the revert's own audit entry). It marks the
// original entry reverted on success. A non-revertable entry returns
// ErrNotRevertable; an already-reverted one returns store.ErrConflict.
func (rv *Reverter) Revert(ctx context.Context, id string) (string, error) {
	entry, err := rv.store.GetAuditLog(ctx, id)
	if err != nil {
		return "", err
	}
	if !entry.Revertable {
		return "", ErrNotRevertable
	}
	if entry.RevertedAt != nil {
		return "", store.ErrConflict
	}

	var meta metaShape
	if len(entry.Metadata) > 0 {
		if err := json.Unmarshal(entry.Metadata, &meta); err != nil {
			return "", fmt.Errorf("%w: metadata: %v", ErrNotRevertable, err)
		}
	}
	if len(meta.Before) == 0 {
		return "", fmt.Errorf("%w: no before-snapshot", ErrNotRevertable)
	}

	summary, err := rv.apply(ctx, entry, meta.Before)
	if err != nil {
		return "", err
	}
	// Stamp the original entry reverted last: if MarkAuditReverted races and
	// reports a conflict, the inverse was still applied (idempotent reapply of a
	// snapshot), so the operator's intent held.
	if err := rv.store.MarkAuditReverted(ctx, id); err != nil {
		return "", err
	}
	return summary, nil
}

// apply dispatches on the recorded action and reapplies the before-snapshot.
func (rv *Reverter) apply(ctx context.Context, entry store.AuditLog, before json.RawMessage) (string, error) {
	switch {
	case ActionMatches(entry.Action, "PUT", "/apps/{id}/env"):
		return rv.revertEnv(ctx, TargetID(entry), before)
	case ActionMatches(entry.Action, "PUT", "/instance/base-domain"):
		return rv.revertBaseDomain(ctx, before)
	case ActionMatches(entry.Action, "PATCH", "/apps/{id}"):
		return rv.revertApp(ctx, TargetID(entry), before)
	default:
		return "", fmt.Errorf("%w: %s", ErrNotRevertable, entry.Action)
	}
}

// --- env vars ---

// EnvEntry is one env-var row in an env before-snapshot. Value is base64 of the
// *sealed* bytes — the audit_log is not encrypted, so plaintext never lands here.
// Shared with the auditdiff builder so the before-shape can't drift.
type EnvEntry struct {
	Key       string `json:"key"`
	Value     string `json:"value"` // base64 of the sealed bytes
	Sensitive bool   `json:"sensitive"`
	WriteOnly bool   `json:"write_only"`
}

// EnvSnapshot is the full prior env set captured before a replace.
type EnvSnapshot struct {
	Env []EnvEntry `json:"env"`
}

func (rv *Reverter) revertEnv(ctx context.Context, appID string, before json.RawMessage) (string, error) {
	if appID == "" {
		return "", fmt.Errorf("%w: env snapshot has no app target", ErrNotRevertable)
	}
	var snap EnvSnapshot
	if err := json.Unmarshal(before, &snap); err != nil {
		return "", fmt.Errorf("%w: env snapshot: %v", ErrNotRevertable, err)
	}
	inputs := make([]store.EnvVarInput, 0, len(snap.Env))
	for _, e := range snap.Env {
		sealed, err := base64.StdEncoding.DecodeString(e.Value)
		if err != nil {
			return "", fmt.Errorf("%w: env value decode: %v", ErrNotRevertable, err)
		}
		inputs = append(inputs, store.EnvVarInput{Key: e.Key, Value: sealed, Sensitive: e.Sensitive, WriteOnly: e.WriteOnly})
	}
	if err := rv.store.ReplaceEnvVars(ctx, appID, inputs); err != nil {
		return "", err
	}
	return fmt.Sprintf("restored %d environment variable(s)", len(inputs)), nil
}

// --- base domain ---

// BaseDomainSnapshot is the prior base-domain string captured before a change.
type BaseDomainSnapshot struct {
	BaseDomain string `json:"base_domain"`
}

func (rv *Reverter) revertBaseDomain(ctx context.Context, before json.RawMessage) (string, error) {
	var snap BaseDomainSnapshot
	if err := json.Unmarshal(before, &snap); err != nil {
		return "", fmt.Errorf("%w: base-domain snapshot: %v", ErrNotRevertable, err)
	}
	if err := rv.store.SetBaseDomain(ctx, snap.BaseDomain); err != nil {
		return "", err
	}
	if rv.baseDom != nil {
		rv.baseDom.SetBaseDomain(snap.BaseDomain)
	}
	if snap.BaseDomain == "" {
		return "cleared base domain override", nil
	}
	return "restored base domain to " + snap.BaseDomain, nil
}

// --- app config ---

// AppFields is the prior app-config field set; pointer fields distinguish
// "absent" from a zero value when reapplying a partial patch.
type AppFields struct {
	Name        *string         `json:"name"`
	GitURL      *string         `json:"git_url"`
	GitBranch   *string         `json:"git_branch"`
	ComposeFile *string         `json:"compose_file"`
	BuildKind   *string         `json:"build_kind"`
	BuildConfig json.RawMessage `json:"build_config"`
	MemLimitMB  *int            `json:"mem_limit_mb"`
}

// AppSnapshot wraps the prior app-config fields captured before a PATCH.
type AppSnapshot struct {
	App AppFields `json:"app"`
}

func (rv *Reverter) revertApp(ctx context.Context, appID string, before json.RawMessage) (string, error) {
	if appID == "" {
		return "", fmt.Errorf("%w: app snapshot has no app target", ErrNotRevertable)
	}
	var snap AppSnapshot
	if err := json.Unmarshal(before, &snap); err != nil {
		return "", fmt.Errorf("%w: app snapshot: %v", ErrNotRevertable, err)
	}
	a := snap.App
	// MemLimitMB on UpdateApp uses 0=clear, nil=unchanged. The snapshot stores the
	// prior pointer (nil = was unlimited), so translate nil→0 to actively clear it.
	mem := 0
	if a.MemLimitMB != nil {
		mem = *a.MemLimitMB
	}
	if _, err := rv.store.UpdateApp(ctx, appID, a.Name, a.GitURL, a.GitBranch, a.ComposeFile, a.BuildKind, a.BuildConfig, &mem); err != nil {
		return "", err
	}
	return "restored app configuration", nil
}

// ActionMatches reports whether an audit Action ("PUT /api/apps/{id}/env") has
// the given method and route suffix. Matching on suffix keeps it robust to the
// /api mount prefix that chi includes in the recorded route pattern. Exported so
// the auditdiff builder shares the exact same action-dispatch logic.
func ActionMatches(action, method, suffix string) bool {
	m, path, ok := strings.Cut(action, " ")
	return ok && m == method && strings.HasSuffix(path, suffix)
}

// TargetID returns the audit entry's recorded target id, or "".
func TargetID(entry store.AuditLog) string {
	if entry.TargetID != nil {
		return *entry.TargetID
	}
	return ""
}
