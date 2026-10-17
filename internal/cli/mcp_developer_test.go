package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestServeDeveloperOptions(t *testing.T) {
	root := t.TempDir()
	cmd, err := parse([]string{"serve", "--developer-tools", "--allow-guest-write", "--developer-workspace", root, "--allow-vm-write"})
	if err != nil || !cmd.flags["developer-tools"] || !cmd.flags["allow-guest-write"] || !cmd.flags["allow-vm-write"] || cmd.values["developer-workspace"] != root {
		t.Fatal(cmd, err)
	}
	for _, args := range [][]string{
		{"vm", "list", "--developer-tools"}, {"guest", "processes", "vm", "--allow-guest-write"}, {"serve", "--developer-tools=true"}, {"serve", "--developer-workspace"},
	} {
		if _, err := parse(args); err == nil {
			t.Fatal("invalid developer flags accepted")
		}
	}
}

func TestServeDeveloperConfigurationErrorsUseOnlyStderr(t *testing.T) {
	for _, args := range [][]string{
		{"serve", "--allow-guest-write"}, {"serve", "--developer-workspace", t.TempDir()},
		{"serve", "--developer-tools", "--developer-workspace="},
		{"serve", "--developer-tools", "--developer-workspace", filepath.Join(t.TempDir(), "missing")},
	} {
		var stdout, stderr bytes.Buffer
		if status := Run(context.Background(), args, &stdout, &stderr); status != 1 || stdout.Len() != 0 {
			t.Fatalf("status=%d stdout=%q", status, stdout.String())
		}
		var payload map[string]any
		if err := json.Unmarshal(stderr.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload["ok"] != false {
			t.Fatal(payload)
		}
	}
}
