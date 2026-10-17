package bridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const syntheticWorkflowVM = "11111111-1111-4111-8111-111111111111"

func developmentProfileFixture(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "synthetic-profile.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDevelopmentWorkflowCheckpointsAndReferences(t *testing.T) {
	profile := DevelopmentProfile{Version: 1, VM: syntheticWorkflowVM, Steps: []DevelopmentStep{
		{Name: "baseline", Action: "vm.snapshot-create", Params: map[string]any{"name": "Synthetic baseline"}},
		{Name: "restore", Action: "vm.snapshot-restore", Params: map[string]any{"snapshot": map[string]any{"$ref": "baseline.data.stdout"}}},
	}, Cleanup: &DevelopmentStep{Action: "vm.recycle", Params: map[string]any{"mode": "stop"}}}
	f := newFixture(t)
	f.response.stdout = "Synthetic snapshot"
	p := map[string]any{"profile_file": developmentProfileFixture(t, profile), "output_directory": t.TempDir(), "confirm_vm": syntheticWorkflowVM}
	result, err := f.bridge.runDevelopmentProfile(context.Background(), syntheticWorkflowVM, p, false, true)
	if err != nil {
		t.Fatal(err)
	}
	data := mapValue(t, result["data"])
	if f.calls != 3 || data["cleanup"] != "completed" {
		t.Fatalf("calls=%d result=%v", f.calls, data)
	}
	report, err := os.ReadFile(data["report_file"].(string))
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if json.Unmarshal(report, &parsed) != nil || parsed["status"] != "completed" {
		t.Fatal("invalid report")
	}
}

func TestDevelopmentWorkflowFailsBeforeConfirmationOrWrongTarget(t *testing.T) {
	profile := DevelopmentProfile{Version: 1, VM: syntheticWorkflowVM, Steps: []DevelopmentStep{{Name: "start", Action: "vm.start"}}, Cleanup: &DevelopmentStep{Action: "vm.recycle", Params: map[string]any{"mode": "delete"}}}
	f := newFixture(t)
	p := map[string]any{"profile_file": developmentProfileFixture(t, profile), "output_directory": t.TempDir()}
	_, err := f.bridge.runDevelopmentProfile(context.Background(), syntheticWorkflowVM, p, false, true)
	checkCode(t, err, "CONFIRMATION_REQUIRED")
	p["confirm_vm"] = syntheticWorkflowVM
	_, err = f.bridge.runDevelopmentProfile(context.Background(), "22222222-2222-4222-8222-222222222222", p, false, true)
	checkCode(t, err, "INVALID_PROFILE")
	if f.calls != 0 {
		t.Fatal("invalid workflow reached backend")
	}
}

func TestDevelopmentWorkflowStopsFailedStageAndPreservesReport(t *testing.T) {
	profile := DevelopmentProfile{Version: 1, VM: syntheticWorkflowVM, Steps: []DevelopmentStep{{Name: "start", Action: "vm.start"}, {Name: "later", Action: "vm.stop"}}}
	f := newFixture(t)
	f.response = commandResult{exitCode: 7, stderr: "Synthetic failure"}
	p := map[string]any{"profile_file": developmentProfileFixture(t, profile), "output_directory": t.TempDir()}
	_, err := f.bridge.runDevelopmentProfile(context.Background(), syntheticWorkflowVM, p, false, true)
	failure := checkCode(t, err, "WORKFLOW_FAILED")
	if f.calls != 1 || failure.Details["failed_stage"] != "start" {
		t.Fatal("workflow continued after failed stage")
	}
	if _, err := os.Stat(failure.Details["report_file"].(string)); err != nil {
		t.Fatal("failed workflow lost report")
	}
}

func TestDevelopmentProfileRejectsAmbiguousOrRecursiveInput(t *testing.T) {
	for _, data := range []string{
		`{"version":1,"version":1,"vm":"11111111-1111-4111-8111-111111111111","steps":[{"name":"test","action":"vm.info"}]}`,
		`{"version":2,"vm":"11111111-1111-4111-8111-111111111111","steps":[{"name":"test","action":"vm.info"}]}`,
		`{"version":1,"vm":"SyntheticName","steps":[{"name":"test","action":"vm.info"}]}`,
		`{"version":1,"vm":"11111111-1111-4111-8111-111111111111","steps":[{"name":"test","action":"workflow.run"}]}`,
		`{"version":1,"vm":"11111111-1111-4111-8111-111111111111","steps":[],"unknown":true}`,
	} {
		path := filepath.Join(t.TempDir(), "synthetic.json")
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadDevelopmentProfile(path); err == nil {
			t.Fatal("accepted invalid profile")
		}
	}
}

func TestDevelopmentReferencesRejectForwardAndMixedObjects(t *testing.T) {
	for _, value := range []any{map[string]any{"$ref": "future.data.pid"}, map[string]any{"$ref": "run.data.pid", "extra": true}} {
		if _, err := resolveDevelopmentReferences(value, map[string]any{}); err == nil {
			t.Fatal("accepted invalid reference")
		}
	}
}
