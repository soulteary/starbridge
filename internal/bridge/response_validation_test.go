package bridge

import (
	"context"
	"encoding/json"
	"testing"
)

func validGuestData(action string) map[string]any {
	switch action {
	case "processes.list":
		return map[string]any{"processes": []any{}, "truncated": false, "limit": 500}
	case "windows.list":
		return map[string]any{"windows": []any{}, "truncated": false, "limit": 500, "coordinate_space": "guest-screen-physical-pixels"}
	default:
		return map[string]any{"nodes": []any{}, "hwnd": "0x123", "snapshot_id": "snapshot", "view": "control", "max_depth": 6, "max_nodes": 300,
			"node_limit_reached": false, "depth_limit_reached": false, "warnings": []any{}, "coordinate_space": "guest-screen-physical-pixels"}
	}
}

func TestGuestEnvelopeRejectsAmbiguousOrUnusableResponses(t *testing.T) {
	for _, payload := range []string{
		`{"ok":true,"data":null}`, `{"ok":true,"data":[]}`, `{"ok":true,"data":{},"error":null}`,
		`{"ok":false,"error":{}}`, `{"ok":false,"error":{"code":"","message":"failure"}}`,
		`{"ok":false,"error":{"code":"FAIL","message":" "}}`, `{"ok":false,"error":{"code":1,"message":"failure"}}`,
		`{"ok":false,"error":{"code":"FAIL","message":"failure"},"data":null}`,
	} {
		t.Run(payload, func(t *testing.T) {
			f := newFixture(t)
			f.response.stdout = payload
			_, err := f.bridge.Guest(context.Background(), "vm", "processes.list", nil)
			checkCode(t, err, "INVALID_GUEST_RESPONSE")
		})
	}
}

func TestGuestActionResponsesValidateFieldsAndPreserveExtensions(t *testing.T) {
	for _, action := range []string{"processes.list", "windows.list", "ui.tree"} {
		t.Run(action, func(t *testing.T) {
			data := validGuestData(action)
			data["future_field"] = map[string]any{"retained": true}
			if err := guestData(action, data); err != nil {
				t.Fatal(err)
			}
			for field := range data {
				if field == "future_field" {
					continue
				}
				copy := map[string]any{}
				for key, value := range data {
					copy[key] = value
				}
				delete(copy, field)
				if err := guestData(action, copy); err == nil {
					t.Errorf("accepted missing %s", field)
				}
				copy[field] = "wrong type"
				if err := guestData(action, copy); err == nil {
					// String fields already have the correct type; test null instead.
					copy[field] = nil
					if err := guestData(action, copy); err == nil {
						t.Errorf("accepted invalid %s", field)
					}
				}
			}
			f := newFixture(t)
			payload, _ := json.Marshal(map[string]any{"ok": true, "data": data, "future_envelope": true,
				"meta": map[string]any{"protocol_version": "1", "action": action, "session_id": 1, "user_interactive": true}})
			f.response.stdout = string(payload)
			params := map[string]any{}
			if action == "ui.tree" {
				params["hwnd"] = "0x123"
			}
			result, err := f.bridge.Guest(context.Background(), "vm", action, params)
			if err != nil || result["future_envelope"] != true {
				t.Fatalf("compatible response lost: %#v, %v", result, err)
			}
			if mapValue(t, result["data"])["future_field"] == nil {
				t.Fatal("extension was discarded")
			}
		})
	}
}

func TestGuestResponseItems(t *testing.T) {
	for _, test := range []struct {
		action, field string
		item          map[string]any
	}{
		{"processes.list", "processes", map[string]any{"pid": 12, "name": "程序", "path": nil}},
		{"windows.list", "windows", map[string]any{"hwnd": "0x123", "pid": 12, "title": "", "class_name": "Window", "is_visible": true,
			"rect": map[string]any{"x": -10, "y": 0, "width": 640, "height": 480}}},
		{"ui.tree", "nodes", map[string]any{"node_id": "n1", "parent_id": nil, "depth": 0, "name": "按钮", "control_type": "Button", "pid": 12,
			"rect": nil, "is_enabled": true, "is_offscreen": false, "is_password": false}},
	} {
		t.Run(test.action, func(t *testing.T) {
			data := validGuestData(test.action)
			data[test.field] = []any{test.item}
			if err := guestData(test.action, data); err != nil {
				t.Fatal(err)
			}
			data[test.field] = []any{nil}
			if err := guestData(test.action, data); err == nil {
				t.Fatal("accepted null item")
			}
			data[test.field] = []any{map[string]any{}}
			if err := guestData(test.action, data); err == nil {
				t.Fatal("accepted missing item fields")
			}
		})
	}
}
