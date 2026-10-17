package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func boundedGuestData(action string, count, depth int) map[string]any {
	data := validGuestData(action)
	items := make([]any, 0, count)
	field := "processes"
	for index := 0; index < count; index++ {
		var item map[string]any
		switch action {
		case "processes.list":
			item = map[string]any{"pid": index + 1, "name": "program"}
		case "windows.list":
			field = "windows"
			item = map[string]any{"hwnd": fmt.Sprint(index + 1), "pid": 12, "title": "Window", "class_name": "Window", "is_visible": true,
				"rect": map[string]any{"x": 0, "y": 0, "width": 640, "height": 480}}
		case "ui.tree":
			field = "nodes"
			item = map[string]any{"node_id": fmt.Sprintf("n%d", index), "parent_id": nil, "depth": depth, "name": "Button", "control_type": "Button", "pid": 12,
				"rect": nil, "is_enabled": true, "is_offscreen": false, "is_password": false}
		}
		items = append(items, item)
	}
	data[field] = items
	return data
}

func TestGuestResponseDeclaredBounds(t *testing.T) {
	for _, test := range []struct {
		name, action, field string
		bound, count, depth int
		valid               bool
	}{
		{"process count at limit", "processes.list", "limit", 1, 1, 0, true},
		{"process count above limit", "processes.list", "limit", 1, 2, 0, false},
		{"process count above global cap", "processes.list", "limit", 1000, 1001, 0, false},
		{"window count at limit", "windows.list", "limit", 1, 1, 0, true},
		{"window count above limit", "windows.list", "limit", 1, 2, 0, false},
		{"node count at limit", "ui.tree", "max_nodes", 1, 1, 0, true},
		{"node count above limit", "ui.tree", "max_nodes", 1, 2, 0, false},
		{"root at depth zero", "ui.tree", "max_depth", 0, 1, 0, true},
		{"node beyond depth zero", "ui.tree", "max_depth", 0, 1, 1, false},
		{"node at maximum depth", "ui.tree", "max_depth", 12, 1, 12, true},
		{"node beyond declared depth", "ui.tree", "max_depth", 11, 1, 12, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := boundedGuestData(test.action, test.count, test.depth)
			data[test.field] = test.bound
			if test.action != "ui.tree" {
				data["truncated"] = true // Truncation never permits returning extra items.
			}
			err := guestData(test.action, data)
			if test.valid {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				checkCode(t, err, "INVALID_GUEST_RESPONSE")
			}
		})
	}
}

func TestGuestResponseRespectsRequestedBounds(t *testing.T) {
	for _, test := range []struct {
		name, action string
		params, caps map[string]any
		count, depth int
		valid        bool
	}{
		{"process requested boundary", "processes.list", map[string]any{"limit": 1}, map[string]any{"limit": 1}, 1, 0, true},
		{"process inflated limit", "processes.list", map[string]any{"limit": 1}, map[string]any{"limit": 2}, 2, 0, false},
		{"process inflated declaration alone", "processes.list", map[string]any{"limit": 1}, map[string]any{"limit": 2}, 1, 0, false},
		{"window inflated limit", "windows.list", map[string]any{"limit": 1}, map[string]any{"limit": 2}, 2, 0, false},
		{"window requested boundary", "windows.list", map[string]any{"limit": 1}, map[string]any{"limit": 1}, 1, 0, true},
		{"node inflated count", "ui.tree", map[string]any{"max_nodes": 1}, map[string]any{"max_nodes": 2}, 2, 0, false},
		{"node inflated depth", "ui.tree", map[string]any{"max_depth": 0}, map[string]any{"max_depth": 1}, 1, 1, false},
		{"node inflated depth declaration alone", "ui.tree", map[string]any{"max_depth": 0}, map[string]any{"max_depth": 1}, 1, 0, false},
		{"root requested boundary", "ui.tree", map[string]any{"max_depth": 0, "max_nodes": 1}, map[string]any{"max_depth": 0, "max_nodes": 1}, 1, 0, true},
		{"stricter helper bounds", "ui.tree", map[string]any{"max_depth": 2, "max_nodes": 2}, map[string]any{"max_depth": 0, "max_nodes": 1}, 1, 0, true},
		{"default list boundary", "processes.list", nil, map[string]any{"limit": 500}, 500, 0, true},
		{"inflated default list cap", "processes.list", nil, map[string]any{"limit": 501}, 501, 0, false},
		{"inflated default node cap", "ui.tree", nil, map[string]any{"max_nodes": 301}, 301, 0, false},
		{"inflated default depth cap", "ui.tree", nil, map[string]any{"max_depth": 7}, 1, 7, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			data := boundedGuestData(test.action, test.count, test.depth)
			for key, value := range test.caps {
				data[key] = value
			}
			payload, err := json.Marshal(map[string]any{"ok": true, "data": data,
				"meta": map[string]any{"protocol_version": "1", "action": test.action, "session_id": 1, "user_interactive": true}})
			if err != nil {
				t.Fatal(err)
			}
			f.response.stdout = string(payload)
			params := map[string]any{}
			for key, value := range test.params {
				params[key] = value
			}
			if test.action == "ui.tree" {
				params["hwnd"] = "0x123"
			}
			result, err := f.bridge.Guest(context.Background(), "vm", test.action, params)
			if test.valid {
				if err != nil || result["ok"] != true {
					t.Fatalf("result=%#v err=%v", result, err)
				}
			} else {
				checkCode(t, err, "INVALID_GUEST_RESPONSE")
			}
			if f.calls != 1 {
				t.Fatalf("calls=%d, want one helper request", f.calls)
			}
		})
	}
}
