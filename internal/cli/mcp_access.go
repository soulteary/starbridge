package cli

import (
	"context"
	"strings"

	"github.com/soulteary/starbridge/internal/bridge"
	"github.com/soulteary/starbridge/internal/mcpserver"
)

func runMCP(ctx context.Context, backend *bridge.Bridge, cmd command) error {
	if path, present := cmd.values["developer-workspace"]; present && strings.TrimSpace(path) == "" {
		return invalid("--developer-workspace requires a nonempty directory path.")
	}
	var policy *mcpserver.AccessPolicy
	if path, present := cmd.values["access-policy"]; present {
		var err error
		policy, err = mcpserver.LoadAccessPolicy(path)
		if err != nil {
			return err
		}
	}
	return mcpserver.RunWithOptions(ctx, backend, mcpserver.ServerOptions{
		AllowVMWrite: cmd.flags["allow-vm-write"], DeveloperTools: cmd.flags["developer-tools"],
		AllowGuestWrite: cmd.flags["allow-guest-write"], DeveloperWorkspace: cmd.values["developer-workspace"], AccessPolicy: policy,
	})
}
