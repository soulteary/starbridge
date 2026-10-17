package cli

import (
	"context"

	"github.com/soulteary/starbridge/internal/bridge"
	"github.com/soulteary/starbridge/internal/mcpserver"
)

func runMCP(ctx context.Context, backend *bridge.Bridge, cmd command) error {
	var policy *mcpserver.AccessPolicy
	if path, present := cmd.values["access-policy"]; present {
		var err error
		policy, err = mcpserver.LoadAccessPolicy(path)
		if err != nil {
			return err
		}
	}
	return mcpserver.RunWithAccess(ctx, backend, cmd.flags["allow-vm-write"], policy)
}
