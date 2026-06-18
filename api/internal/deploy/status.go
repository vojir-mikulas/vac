// Package deploy owns the deployment pipeline: queue + worker + step
// orchestration + log writer. Status enums for deployments, services, and
// apps live here so the rest of the codebase has one place to look.
package deploy

// Deployment status — the lifecycle of a single deploy attempt.
const (
	DeploymentStatusQueued         = "queued"
	DeploymentStatusCloning        = "cloning"
	DeploymentStatusBuilding       = "building"
	DeploymentStatusDeploying      = "deploying"
	DeploymentStatusHealthChecking = "health-checking"
	DeploymentStatusRunning        = "running"
	DeploymentStatusError          = "error"
	DeploymentStatusInterrupted    = "interrupted"
	// DeploymentStatusCanceled is a user-initiated cancellation (plan 20).
	// Distinct from `interrupted` (which means vac-api restarted mid-deploy) so
	// the timeline reads honestly. Terminal — excluded from the per-app
	// uniqueness guard, so cancelling frees the app to deploy again immediately.
	DeploymentStatusCanceled = "canceled"
	// DeploymentStatusScheduled parks a deploy that arrived outside its app's
	// deploy window (maintenance-mode-and-deploy-gates.md, Phase 3). The sweeper
	// releases it to `queued` when a window opens. NON-terminal / ACTIVE — it
	// counts in the per-app uniqueness guard so parked deploys can't stack.
	DeploymentStatusScheduled = "scheduled"
	// DeploymentStatusPendingApproval holds a deploy on an approval-gated trigger
	// (Phase 4) until an operator approves it (→ `queued`) or rejects it
	// (→ terminal `canceled`). NON-terminal / ACTIVE, same uniqueness rationale.
	DeploymentStatusPendingApproval = "pending-approval"
)

// IsTerminalDeploymentStatus returns true once a deployment has settled.
// MarkInProgressDeploymentsInterrupted only sweeps non-terminal rows.
func IsTerminalDeploymentStatus(s string) bool {
	switch s {
	case DeploymentStatusRunning, DeploymentStatusError, DeploymentStatusInterrupted, DeploymentStatusCanceled:
		return true
	}
	return false
}

// Service status — per-service runtime state. Mirrors mvp.md § Service Status
// Model. The enum is Go-owned (no DB CHECK constraint).
const (
	ServiceStatusCreated   = "created"
	ServiceStatusBuilding  = "building"
	ServiceStatusDeploying = "deploying"
	ServiceStatusRunning   = "running"
	ServiceStatusDegraded  = "degraded"
	ServiceStatusCrashLoop = "crash-loop"
	ServiceStatusStopped   = "stopped"
	ServiceStatusError     = "error"
)

// App / stack status — derived from the services that make up the app.
// Mirrors the same enum so the UI can render either field with the same
// badge palette.
const (
	AppStatusCreated   = "created"
	AppStatusBuilding  = "building"
	AppStatusDeploying = "deploying"
	AppStatusRunning   = "running"
	AppStatusDegraded  = "degraded"
	AppStatusCrashLoop = "crash-loop"
	AppStatusStopped   = "stopped"
	AppStatusError     = "error"
)

// DeriveAppStatus collapses a set of service statuses into the stack-level
// status surfaced on the apps row. Rules per mvp.md § Service Status Model:
//
//	all running         → running
//	any crash-loop      → crash-loop (highest priority)
//	any building        → building
//	any deploying       → deploying
//	any error           → error
//	any stopped/degraded → degraded
//	empty               → created
func DeriveAppStatus(services []string) string {
	if len(services) == 0 {
		return AppStatusCreated
	}
	allRunning := true
	hasBuilding := false
	hasDeploying := false
	hasError := false
	hasDegraded := false
	for _, s := range services {
		if s != ServiceStatusRunning {
			allRunning = false
		}
		switch s {
		case ServiceStatusCrashLoop:
			return AppStatusCrashLoop
		case ServiceStatusBuilding:
			hasBuilding = true
		case ServiceStatusDeploying:
			hasDeploying = true
		case ServiceStatusError:
			hasError = true
		case ServiceStatusStopped, ServiceStatusDegraded:
			hasDegraded = true
		}
	}
	switch {
	case allRunning:
		return AppStatusRunning
	case hasBuilding:
		return AppStatusBuilding
	case hasDeploying:
		return AppStatusDeploying
	case hasError:
		return AppStatusError
	case hasDegraded:
		return AppStatusDegraded
	}
	return AppStatusDegraded
}

// MapPsStateToServiceStatus translates `docker compose ps` State to the VAC
// service-status enum. Docker states observed: "running", "exited", "dead",
// "created", "restarting", "paused".
func MapPsStateToServiceStatus(state string) string {
	switch state {
	case "running":
		return ServiceStatusRunning
	case "exited", "dead":
		return ServiceStatusStopped
	case "restarting":
		return ServiceStatusDeploying
	case "paused":
		return ServiceStatusStopped
	case "created":
		return ServiceStatusCreated
	}
	return ServiceStatusCreated
}
