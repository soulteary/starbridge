package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestServeAccessPolicyOptionAndHelp(t *testing.T) {
	cmd, err := parse([]string{"serve", "--access-policy=policy.json", "--allow-vm-write"})
	if err != nil || cmd.values["access-policy"] != "policy.json" || !cmd.flags["allow-vm-write"] {
		t.Fatalf("parse: %#v %v", cmd, err)
	}
	if _, err := parse([]string{"serve", "--access-policy"}); err == nil {
		t.Fatal("missing policy path accepted")
	}
	if _, err := parse([]string{"vm", "list", "--access-policy=policy.json"}); err == nil {
		t.Fatal("access policy accepted outside serve")
	}
}

func TestServeInvalidAccessPolicyKeepsStdoutClean(t *testing.T) {
	for _, path := range []string{"", filepath.Join(t.TempDir(), "missing.json")} {
		var stdout, stderr bytes.Buffer
		status := Run(context.Background(), []string{"serve", "--access-policy=" + path}, &stdout, &stderr)
		if status != 1 || stdout.Len() != 0 {
			t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
		}
		var payload map[string]any
		if err := json.Unmarshal(stderr.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		assertCode(t, payload, "INVALID_ACCESS_POLICY")
	}
}
