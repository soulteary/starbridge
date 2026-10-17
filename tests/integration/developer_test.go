//go:build desktopintegration

package integration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/png"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/soulteary/starbridge/internal/bridge"
)

func decodeFixtureIdentity(line string, identity *map[string]any) error {
	decoder := json.NewDecoder(strings.NewReader(line))
	decoder.UseNumber()
	return decoder.Decode(identity)
}

// No raw responses, environment values, VM identifiers or runtime paths are
// printed or checked in. This opt-in test owns only its newly created task.
func TestDeveloperDesktopWorkflow(t *testing.T) {
	if os.Getenv("STARBRIDGE_RUN_DESKTOP_ACCEPTANCE") != "1" {
		t.Skip("explicit desktop acceptance opt-in is required")
	}
	vm := os.Getenv("STARBRIDGE_TEST_VM_UUID")
	if !regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(vm) {
		t.Fatal("a dedicated fixture VM UUID must be configured")
	}
	options := bridge.DefaultOptions()
	if path := os.Getenv("STARBRIDGE_TEST_DEVELOPER_SCRIPT"); path != "" {
		options.DeveloperScript = path
	}
	b, err := bridge.New(options)
	if err != nil {
		t.Fatal("invalid integration options")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	call := func(action string, p map[string]any, write bool) map[string]any {
		t.Helper()
		result, err := b.Developer(ctx, vm, action, p, write)
		if err != nil {
			var failure *bridge.Error
			code := "TRANSPORT_ERROR"
			if errors.As(err, &failure) {
				code = failure.Code
			}
			t.Fatalf("fixture action %s failed (%s; raw details withheld)", action, code)
		}
		if result["ok"] != true {
			t.Fatalf("fixture action %s returned failure (raw response withheld)", action)
		}
		data, ok := result["data"].(map[string]any)
		if !ok {
			t.Fatal("invalid fixture response data")
		}
		return data
	}
	call("system.info", map[string]any{}, false)
	started := call("task.start", map[string]any{"profile": "starbridge-fixture", "timeout_ms": 120000}, true)
	taskID, ok := started["task_id"].(string)
	if !ok {
		t.Fatal("fixture task identity missing")
	}
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		_, _ = b.Developer(cleanupCtx, vm, "task.cancel", map[string]any{"task_id": taskID}, true)
	}()
	identity := map[string]any{}
	for ctx.Err() == nil {
		output := call("task.output", map[string]any{"task_id": taskID, "stream": "stdout", "offset": 0, "limit": 16384}, false)
		encoded, _ := output["data_base64"].(string)
		content, decodeErr := base64.StdEncoding.DecodeString(encoded)
		line := strings.SplitN(string(content), "\n", 2)[0]
		if decodeErr == nil && decodeFixtureIdentity(line, &identity) == nil && identity["hwnd"] != nil {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(200 * time.Millisecond):
		}
	}
	if identity["hwnd"] == nil || identity["pid"] == nil || identity["start_time_utc"] == nil {
		t.Fatal("fixture did not publish a usable window identity")
	}
	taskState := call("task.status", map[string]any{"task_id": taskID}, false)
	fixturePID, validPID := identity["pid"].(json.Number)
	taskPID, validTaskPID := taskState["pid"].(json.Number)
	fixtureStart, validStart := identity["start_time_utc"].(string)
	taskStart, validTaskStart := taskState["start_time_utc"].(string)
	fixtureInstant, fixtureTimeError := time.Parse(time.RFC3339Nano, fixtureStart)
	taskInstant, taskTimeError := time.Parse(time.RFC3339Nano, taskStart)
	if !validPID || !validTaskPID || fixturePID != taskPID || !validStart || !validTaskStart || fixtureTimeError != nil || taskTimeError != nil || !fixtureInstant.Equal(taskInstant) {
		t.Fatal("published window identity does not belong to the fixture task process")
	}
	parameters := func() map[string]any {
		return map[string]any{"hwnd": identity["hwnd"], "pid": identity["pid"], "start_time_utc": identity["start_time_utc"]}
	}
	buttonName := "Starbridge 中文"
	query := parameters()
	query["selector"] = map[string]any{"name": buttonName, "control_type": "Button"}
	query["mode"] = "one"
	data := call("ui.query", query, false)
	items, ok := data["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatal("known synthetic button was not uniquely located")
	}
	act := parameters()
	act["selector"] = query["selector"]
	act["operation"] = "invoke"
	call("ui.act", act, true)
	postcondition := parameters()
	postcondition["selector"] = map[string]any{"name": "1", "control_type": "Text"}
	postcondition["mode"] = "list"
	call("wait.until", map[string]any{"action": "ui.query", "params": postcondition, "predicate": map[string]any{"path": "data.items", "op": "nonempty"}, "timeout_ms": 10000, "interval_ms": 200}, false)
	screenshot := call("screenshot.capture", parameters(), false)
	encoded, _ := screenshot["png_base64"].(string)
	pixels, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal("invalid synthetic screenshot encoding")
	}
	config, err := png.DecodeConfig(bytes.NewReader(pixels))
	if err != nil || config.Width < 1 || config.Height < 1 {
		t.Fatal("synthetic window screenshot was empty")
	}
	call("task.close", map[string]any{"task_id": taskID}, true)
	call("wait.until", map[string]any{"action": "task.status", "params": map[string]any{"task_id": taskID}, "predicate": map[string]any{"path": "data.status", "op": "eq", "value": "exited"}, "timeout_ms": 10000, "interval_ms": 200}, false)
}
