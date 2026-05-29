package dockercli

import (
	"context"
	"strings"
)

// NetworkCreate creates a user-defined bridge network. Idempotent: an
// "already exists" error is treated as success so boot reconcile can call it
// unconditionally.
func (c *Compose) NetworkCreate(ctx context.Context, name string) error {
	cmd := c.command(ctx, "", "network", "create", name)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "already exists") {
			return nil
		}
		return mapCmdError(err, out)
	}
	return nil
}

// NetworkConnect attaches a container to a network with the given DNS alias.
// Idempotent: an "already exists in network" / "already connected" error is
// treated as success (redeploys re-attach freshly-created containers).
func (c *Compose) NetworkConnect(ctx context.Context, network, container, alias string) error {
	args := []string{"network", "connect"}
	if alias != "" {
		args = append(args, "--alias", alias)
	}
	args = append(args, network, container)
	cmd := c.command(ctx, "", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := string(out)
		if strings.Contains(msg, "already exists in network") || strings.Contains(msg, "already connected") {
			return nil
		}
		return mapCmdError(err, out)
	}
	return nil
}

// NetworkDisconnect detaches a container from a network. Idempotent: a
// "not connected" / missing-container error is treated as success.
func (c *Compose) NetworkDisconnect(ctx context.Context, network, container string) error {
	cmd := c.command(ctx, "", "network", "disconnect", "-f", network, container)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := string(out)
		if strings.Contains(msg, "is not connected") || strings.Contains(msg, "No such container") || strings.Contains(msg, "not found") {
			return nil
		}
		return mapCmdError(err, out)
	}
	return nil
}
