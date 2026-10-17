package bridge

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type DevelopmentStep struct {
	Name   string         `json:"name"`
	Action string         `json:"action"`
	Params map[string]any `json:"params,omitempty"`
}

type DevelopmentProfile struct {
	Version         int               `json:"version"`
	VM              string            `json:"vm"`
	DeveloperScript string            `json:"developer_script,omitempty"`
	TimeoutSeconds  int               `json:"timeout_seconds,omitempty"`
	Steps           []DevelopmentStep `json:"steps"`
	Cleanup         *DevelopmentStep  `json:"cleanup,omitempty"`
}

var developmentStepName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

func init() {
	for _, action := range []string{"workflow.run", "workflow.cleanup"} {
		name := action
		registerDeveloperAction(DeveloperAction{Name: name, Description: "Execute an explicit versioned project profile with checkpointed results and opt-in cleanup.", Mutation: true,
			Normalize: normalizeWorkflow, ValidateResult: func(d map[string]any) bool { return nonemptyText(d["run_id"]) },
			RedactPreview: func(p map[string]any) map[string]any {
				p["profile_file"], p["output_directory"] = "[redacted]", "[redacted]"
				return p
			},
			Execute: func(ctx context.Context, b *Bridge, vm string, p map[string]any, allow bool) (Result, error) {
				return b.runDevelopmentProfile(ctx, vm, p, name == "workflow.cleanup", allow)
			},
		})
	}
}

func normalizeWorkflow(p map[string]any) (map[string]any, error) {
	for key := range p {
		if key != "profile_file" && key != "output_directory" && key != "confirm_vm" {
			return nil, NewError("INVALID_ARGUMENT", "Unknown workflow field.", nil)
		}
	}
	copy := map[string]any{}
	for _, key := range []string{"profile_file", "output_directory"} {
		text, ok := p[key].(string)
		if !ok || strings.TrimSpace(text) == "" || len(text) > 4096 || strings.ContainsRune(text, 0) {
			return nil, NewError("INVALID_ARGUMENT", key+" must be a nonempty bounded path.", nil)
		}
		copy[key] = text
	}
	if value, present := p["confirm_vm"]; present {
		if !textValue(value) {
			return nil, NewError("INVALID_ARGUMENT", "confirm_vm must be text.", nil)
		}
		copy["confirm_vm"] = value
	}
	return copy, nil
}

func LoadDevelopmentProfile(path string) (*DevelopmentProfile, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, "", NewError("INVALID_PROFILE", "Cannot read project profile.", nil)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(data) > 65536 || uniqueRecyclePolicyKeys(data) != nil {
		return nil, "", NewError("INVALID_PROFILE", "Profile must be unique-key JSON within 64 KiB.", nil)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var profile DevelopmentProfile
	if decoder.Decode(&profile) != nil || decoder.Decode(new(any)) != io.EOF || profile.Version != 1 || len(profile.Steps) == 0 || len(profile.Steps) > 32 {
		return nil, "", NewError("INVALID_PROFILE", "Expected a version 1 profile with 1..32 steps.", nil)
	}
	if _, valid := canonicalRecycleUUID(profile.VM); !valid {
		return nil, "", NewError("INVALID_PROFILE", "Profile VM must be a UUID.", nil)
	}
	if profile.TimeoutSeconds < 0 || profile.TimeoutSeconds > 600 {
		return nil, "", NewError("INVALID_PROFILE", "Profile timeout_seconds must be 1..600 or omitted.", nil)
	}
	if profile.DeveloperScript != "" && !absoluteWindowsPath(profile.DeveloperScript) {
		return nil, "", NewError("INVALID_PROFILE", "developer_script must be an absolute Windows path.", nil)
	}
	seen := map[string]bool{}
	for _, step := range profile.Steps {
		if !developmentStepName.MatchString(step.Name) || seen[step.Name] || !developmentActionAllowed(step.Action) {
			return nil, "", NewError("INVALID_PROFILE", "Step names must be unique and actions must be supported development operations.", nil)
		}
		seen[step.Name] = true
	}
	if profile.Cleanup != nil && profile.Cleanup.Action != "vm.recycle" {
		return nil, "", NewError("INVALID_PROFILE", "Cleanup must be an explicit vm.recycle operation.", nil)
	}
	sum := sha256.Sum256(data)
	return &profile, hex.EncodeToString(sum[:]), nil
}

func developmentActionAllowed(action string) bool {
	switch action {
	case "vm.info", "vm.start", "vm.stop", "vm.suspend", "vm.resume", "vm.clone", "vm.snapshot-create", "vm.snapshot-list", "vm.snapshot-restore", "vm.recycle", "processes.list", "windows.list", "ui.tree":
		return true
	}
	if strings.HasPrefix(action, "workflow.") {
		return false
	}
	for _, spec := range DeveloperActions() {
		if spec.Name == action {
			return true
		}
	}
	return false
}

func resolveDevelopmentReferences(value any, prior map[string]any) (any, error) {
	if object, ok := asObject(value); ok {
		if ref, exists := object["$ref"]; exists {
			path, valid := ref.(string)
			if !valid || len(object) != 1 {
				return nil, NewError("INVALID_PROFILE", "References must contain only a string $ref.", nil)
			}
			result, found := waitValue(prior, path)
			if !found {
				return nil, NewError("INVALID_PROFILE", "Reference must select a completed earlier step.", nil)
			}
			return result, nil
		}
		copy := map[string]any{}
		for key, item := range object {
			next, err := resolveDevelopmentReferences(item, prior)
			if err != nil {
				return nil, err
			}
			copy[key] = next
		}
		return copy, nil
	}
	if array, ok := value.([]any); ok {
		copy := make([]any, len(array))
		for i, item := range array {
			next, err := resolveDevelopmentReferences(item, prior)
			if err != nil {
				return nil, err
			}
			copy[i] = next
		}
		return copy, nil
	}
	return value, nil
}

func writeDevelopmentJSON(path string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(append(encoded, '\n'))
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func (b *Bridge) runDevelopmentProfile(ctx context.Context, vm string, p map[string]any, cleanupOnly, allow bool) (Result, error) {
	profile, hash, err := LoadDevelopmentProfile(p["profile_file"].(string))
	if err != nil {
		return nil, err
	}
	expected, valid := canonicalRecycleUUID(vm)
	configured, _ := canonicalRecycleUUID(profile.VM)
	if !valid || configured != expected {
		return nil, NewError("INVALID_PROFILE", "Requested VM does not match the project profile UUID.", nil)
	}
	confirmation, _ := p["confirm_vm"].(string)
	preflight := append([]DevelopmentStep{}, profile.Steps...)
	if profile.Cleanup != nil {
		preflight = append(preflight, *profile.Cleanup)
	}
	for _, step := range preflight {
		mode, _ := step.Params["mode"].(string)
		if (step.Action == "vm.snapshot-restore" || (step.Action == "vm.recycle" && (mode == "delete" || mode == "restore"))) && confirmation != vm {
			return nil, NewError("CONFIRMATION_REQUIRED", "Restore/delete workflows require confirm_vm to repeat the requested VM exactly before any stage runs.", nil)
		}
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, NewError("IO_ERROR", "Cannot generate run identity.", nil)
	}
	runID := hex.EncodeToString(nonce[:])
	output := filepath.Join(p["output_directory"].(string), runID)
	if err := os.MkdirAll(output, 0700); err != nil {
		return nil, NewError("IO_ERROR", "Cannot create run output directory.", nil)
	}
	copy := *b
	if profile.DeveloperScript != "" {
		copy.options.DeveloperScript = profile.DeveloperScript
	}
	if profile.TimeoutSeconds > 0 {
		copy.options.Timeout = time.Duration(profile.TimeoutSeconds) * time.Second
	}
	steps := profile.Steps
	if cleanupOnly {
		steps = nil
	}
	prior := map[string]any{}
	records := []any{}
	var failed error
	failedStage := ""
	for index, step := range steps {
		if ctx.Err() != nil {
			failed = NewError("INTERRUPTED", "Workflow interrupted before next stage.", nil)
			failedStage = step.Name
			break
		}
		params, refErr := resolveDevelopmentReferences(step.Params, prior)
		if step.Params == nil {
			params = map[string]any{}
		}
		var result Result
		start := time.Now()
		if refErr != nil {
			err = refErr
		} else {
			object, _ := asObject(params)
			result, err = copy.developmentStep(ctx, vm, step.Action, object, p, allow)
		}
		if err != nil {
			result = Failure(err)
		}
		record := map[string]any{"name": step.Name, "action": step.Action, "started_at": start.UTC().Format(time.RFC3339Nano), "duration_ms": time.Since(start).Milliseconds(), "result": result}
		if writeErr := writeDevelopmentJSON(filepath.Join(output, fmt.Sprintf("%02d-%s.json", index+1, step.Name)), record); writeErr != nil {
			failed = NewError("IO_ERROR", "Cannot checkpoint stage result.", nil)
			failedStage = step.Name
			break
		}
		records = append(records, record)
		prior[step.Name] = map[string]any(result)
		if result["ok"] != true {
			failed = NewError("WORKFLOW_STAGE_FAILED", "A project stage failed.", nil)
			failedStage = step.Name
			break
		}
	}
	cleanup := "not_configured"
	if profile.Cleanup != nil {
		cleanup = "pending"
		if ctx.Err() == nil {
			result, cleanErr := copy.developmentStep(ctx, vm, profile.Cleanup.Action, profile.Cleanup.Params, p, allow)
			if cleanErr != nil {
				result = Failure(cleanErr)
			}
			if result["ok"] == true {
				cleanup = "completed"
			} else {
				cleanup = "failed"
				if failed == nil {
					failed = NewError("WORKFLOW_CLEANUP_FAILED", "Project cleanup failed.", nil)
					failedStage = "cleanup"
				}
			}
			if saveErr := writeDevelopmentJSON(filepath.Join(output, "cleanup.json"), result); saveErr != nil {
				failed = NewError("IO_ERROR", "Cannot save cleanup result.", nil)
			}
		}
	}
	status := "completed"
	if failed != nil {
		status = "failed"
	}
	report := map[string]any{"run_id": runID, "profile_sha256": hash, "vm": configured, "status": status, "failed_stage": failedStage, "cleanup": cleanup, "stages": records}
	if saveErr := writeDevelopmentJSON(filepath.Join(output, "report.json"), report); saveErr != nil {
		return nil, NewError("IO_ERROR", "Cannot save workflow report.", nil)
	}
	if failed != nil {
		return nil, NewError("WORKFLOW_FAILED", "Project workflow did not complete; inspect its checkpointed report before retrying.", map[string]any{"run_id": runID, "report_file": filepath.Join(output, "report.json"), "failed_stage": failedStage, "cleanup": cleanup})
	}
	return Success(map[string]any{"run_id": runID, "status": status, "report_file": filepath.Join(output, "report.json"), "cleanup": cleanup}), nil
}

func (b *Bridge) developmentStep(ctx context.Context, vm, action string, params map[string]any, request map[string]any, allow bool) (Result, error) {
	if !allow {
		return nil, NewError("WRITE_NOT_ALLOWED", "Project execution requires write authorization.", nil)
	}
	allowed := map[string][]string{
		"vm.info": {}, "vm.start": {}, "vm.stop": {}, "vm.suspend": {}, "vm.resume": {}, "vm.snapshot-list": {},
		"vm.clone": {"name"}, "vm.snapshot-create": {"name"}, "vm.snapshot-restore": {"snapshot"}, "vm.recycle": {"mode", "snapshot"},
	}
	if fields, exists := allowed[action]; exists {
		for key, value := range params {
			found := false
			for _, field := range fields {
				if field == key {
					found = true
					break
				}
			}
			if !found || !textValue(value) {
				return nil, NewError("INVALID_PROFILE", "VM stage has unknown or non-text parameters.", nil)
			}
		}
		if action == "vm.recycle" && params["mode"] != nil && params["mode"] != "stop" && params["mode"] != "restore" && params["mode"] != "delete" {
			return nil, NewError("INVALID_PROFILE", "Unsupported recycle mode.", nil)
		}
	}
	var value any
	var err error
	text := func(key string) string { value, _ := params[key].(string); return value }
	confirm, _ := request["confirm_vm"].(string)
	switch action {
	case "vm.info":
		value, err = b.VMInfo(ctx, vm)
	case "vm.start", "vm.stop", "vm.suspend", "vm.resume":
		value, err = b.VMAction(ctx, strings.TrimPrefix(action, "vm."), vm, false, "")
	case "vm.clone":
		value, err = b.VMClone(ctx, vm, text("name"))
	case "vm.snapshot-create":
		value, err = b.SnapshotCreate(ctx, vm, text("name"))
	case "vm.snapshot-list":
		value, err = b.SnapshotList(ctx, vm)
	case "vm.snapshot-restore":
		value, err = b.SnapshotRestore(ctx, vm, text("snapshot"), confirm)
	case "vm.recycle":
		value, err = b.Recycle(ctx, vm, RecycleOptions{Mode: text("mode"), Snapshot: text("snapshot"), Confirmation: confirm})
	case "processes.list", "windows.list", "ui.tree":
		return b.Guest(ctx, vm, action, params)
	default:
		return b.Developer(ctx, vm, action, params, allow)
	}
	if err != nil {
		return nil, err
	}
	return Success(value), nil
}
