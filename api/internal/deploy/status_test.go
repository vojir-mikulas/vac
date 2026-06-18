package deploy_test

import (
	"testing"

	"github.com/vojir-mikulas/vac/api/internal/deploy"
)

func TestDeriveAppStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   []string
		want string
	}{
		{"empty", nil, deploy.AppStatusCreated},
		{"all-running", []string{deploy.ServiceStatusRunning, deploy.ServiceStatusRunning}, deploy.AppStatusRunning},
		{"crash-loop-wins", []string{deploy.ServiceStatusRunning, deploy.ServiceStatusCrashLoop, deploy.ServiceStatusError}, deploy.AppStatusCrashLoop},
		{"any-building", []string{deploy.ServiceStatusRunning, deploy.ServiceStatusBuilding}, deploy.AppStatusBuilding},
		{"any-deploying", []string{deploy.ServiceStatusRunning, deploy.ServiceStatusDeploying}, deploy.AppStatusDeploying},
		{"any-error", []string{deploy.ServiceStatusRunning, deploy.ServiceStatusError}, deploy.AppStatusError},
		{"any-stopped", []string{deploy.ServiceStatusRunning, deploy.ServiceStatusStopped}, deploy.AppStatusDegraded},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := deploy.DeriveAppStatus(tc.in); got != tc.want {
				t.Errorf("DeriveAppStatus(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestMapPsStateToServiceStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want string
	}{
		{"running", deploy.ServiceStatusRunning},
		{"exited", deploy.ServiceStatusStopped},
		{"dead", deploy.ServiceStatusStopped},
		{"restarting", deploy.ServiceStatusDeploying},
		{"paused", deploy.ServiceStatusStopped},
		{"created", deploy.ServiceStatusCreated},
		{"surprise", deploy.ServiceStatusCreated},
	}
	for _, tc := range tests {
		if got := deploy.MapPsStateToServiceStatus(tc.in); got != tc.want {
			t.Errorf("MapPsStateToServiceStatus(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsTerminalDeploymentStatus(t *testing.T) {
	t.Parallel()
	terminals := []string{deploy.DeploymentStatusRunning, deploy.DeploymentStatusError, deploy.DeploymentStatusInterrupted}
	for _, s := range terminals {
		if !deploy.IsTerminalDeploymentStatus(s) {
			t.Errorf("%q should be terminal", s)
		}
	}
	// canceled is also terminal (frees the app to deploy again immediately).
	if !deploy.IsTerminalDeploymentStatus(deploy.DeploymentStatusCanceled) {
		t.Errorf("%q should be terminal", deploy.DeploymentStatusCanceled)
	}
	// scheduled (Phase 3) and pending-approval (Phase 4) are NON-terminal /
	// ACTIVE: they count in the per-app uniqueness guard so parked/pending
	// deploys can't stack, and the boot interrupt-sweep must leave them alone.
	nonTerminals := []string{
		deploy.DeploymentStatusQueued, deploy.DeploymentStatusCloning, deploy.DeploymentStatusBuilding,
		deploy.DeploymentStatusDeploying, deploy.DeploymentStatusHealthChecking,
		deploy.DeploymentStatusScheduled, deploy.DeploymentStatusPendingApproval,
	}
	for _, s := range nonTerminals {
		if deploy.IsTerminalDeploymentStatus(s) {
			t.Errorf("%q should not be terminal", s)
		}
	}
}
