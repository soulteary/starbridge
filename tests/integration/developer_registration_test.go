package integration

import (
	"path/filepath"
	"testing"

	"github.com/soulteary/starbridge/internal/bridge"
)

// These assembly checks run without the desktop opt-in or any VM access, so a
// missing module fails before a dedicated acceptance runner is provisioned.
func TestDeveloperAcceptanceActionsInstalled(t *testing.T) {
	installed := map[string]bool{}
	for _, action := range bridge.DeveloperActions() {
		installed[action.Name] = true
	}
	for _, name := range []string{"system.info", "task.start", "task.status", "task.output", "task.cancel", "task.close", "ui.query", "ui.act", "wait.until", "screenshot.capture"} {
		if !installed[name] {
			t.Errorf("desktop acceptance requires action %s", name)
		}
	}
}

func TestShippedDevelopmentProfileLoads(t *testing.T) {
	if _, _, err := bridge.LoadDevelopmentProfile(filepath.Join("..", "..", "examples", "development-profile.json")); err != nil {
		t.Fatalf("shipped development profile is unsupported: %v", err)
	}
}
