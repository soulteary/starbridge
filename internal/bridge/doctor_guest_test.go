package bridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestDoctorVMReadiness(t *testing.T) {
	for _, scenario := range []string{"ready", "empty_windows", "guest_error", "host_error", "invalid_window", "empty_ui", "invalid_ui"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			calls := []string{}
			f.bridge.runner = func(_ context.Context, _ string, args []string) (commandResult, error) {
				calls = append(calls, args[0])
				if args[0] == "--version" {
					if scenario == "host_error" {
						return commandResult{exitCode: 1}, nil
					}
					return commandResult{stdout: "prlctl test"}, nil
				}
				if args[0] == "list" {
					return commandResult{stdout: `[{"uuid":"vm","status":"running"}]`}, nil
				}
				if args[0] != "exec" {
					t.Fatalf("unexpected mutation: %v", args)
				}
				decoded, _ := base64.StdEncoding.DecodeString(args[len(args)-1])
				var request struct {
					Action string
					Params map[string]any
				}
				if err := json.Unmarshal(decoded, &request); err != nil {
					t.Fatal(err)
				}
				if scenario == "guest_error" {
					return commandResult{stdout: `{"ok":false,"error":{"code":"INTERACTIVE_SESSION_REQUIRED","message":"No session"}}`, exitCode: 1}, nil
				}
				data := map[string]any{"processes": []any{}, "truncated": false, "limit": 1}
				if request.Action == "windows.list" {
					items := []any{map[string]any{"hwnd": "0x123", "pid": 42, "title": "fixture", "class_name": "fixture", "is_visible": true, "rect": map[string]any{"x": 0, "y": 0, "width": 100, "height": 100}}}
					if scenario == "empty_windows" {
						items = []any{}
					}
					if scenario == "invalid_window" {
						items = []any{map[string]any{}}
					}
					data = map[string]any{"windows": items, "truncated": false, "limit": 1, "coordinate_space": "guest-screen-physical-pixels"}
				}
				if request.Action == "ui.tree" {
					if request.Params["max_nodes"] != float64(1) || request.Params["max_depth"] != float64(0) {
						t.Fatal(request.Params)
					}
					nodes := []any{map[string]any{"node_id": "n000001", "parent_id": nil, "depth": 0, "name": "fixture", "control_type": "Window", "pid": 42, "rect": nil, "is_enabled": true, "is_offscreen": false, "is_password": false}}
					if scenario == "empty_ui" {
						nodes = []any{}
					}
					if scenario == "invalid_ui" {
						nodes = []any{nil}
					}
					data = map[string]any{"nodes": nodes, "warnings": []any{}, "hwnd": "0x123", "snapshot_id": "fixture", "view": "control", "max_depth": 0, "max_nodes": 1, "node_limit_reached": false, "depth_limit_reached": true, "coordinate_space": "guest-screen-physical-pixels"}
				}
				payload, _ := json.Marshal(Result{"ok": true, "data": data, "meta": map[string]any{"protocol_version": "1", "action": request.Action, "session_id": 1, "user_interactive": true}})
				return commandResult{stdout: string(payload)}, nil
			}
			value, err := f.bridge.DoctorVM(context.Background(), "vm")
			if err != nil {
				t.Fatal(err)
			}
			report := mapValue(t, value)
			if report["ready"] != (scenario == "ready") {
				t.Fatalf("%#v", report)
			}
			if scenario == "host_error" && len(calls) != 1 {
				t.Fatal(calls)
			}
		})
	}
}

func TestDoctorVMDryRunAndInvalidIdentifier(t *testing.T) {
	f := newFixture(t)
	f.bridge.options.DryRun = true
	value, err := f.bridge.DoctorVM(context.Background(), "test")
	if err != nil || f.calls != 0 || mapValue(t, value)["ready"] != false {
		t.Fatal(value, err, f.calls)
	}
	_, err = f.bridge.DoctorVM(context.Background(), "-invalid")
	checkCode(t, err, "INVALID_ARGUMENT")
	if !strings.Contains(value.(map[string]any)["vm"].(string), "test") {
		t.Fatal(value)
	}
}
