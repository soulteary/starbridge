package mcpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/soulteary/starbridge/internal/bridge"
)

func visualMCPFixture(t *testing.T, action string) (bridge.Result, map[string]any, []byte) {
	t.Helper()
	payload, pixels := developerImageFixture(t)
	s := payload["data"].(map[string]any)
	o := map[string]any{"token": base64.StdEncoding.EncodeToString(make([]byte, 64)), "expires_at": "2026-01-02T03:05:30.1Z", "dpi": 144, "session_id": 1, "window_rect": map[string]any{"x": -18, "y": 12, "width": 17, "height": 17}}
	for _, k := range []string{"hwnd", "pid", "start_time_utc", "timestamp", "rect", "width", "height", "sha256", "coordinate_space", "capture_method"} {
		o[k] = s[k]
	}
	d := map[string]any{"action": action, "observation": o, "screenshot": s, "source": "visual", "uia": map[string]any{"status": "unavailable", "complete": false, "error_code": "UIA_UNAVAILABLE"}}
	p := map[string]any{"hwnd": s["hwnd"], "pid": s["pid"], "start_time_utc": s["start_time_utc"]}
	if action == "ui.click" {
		d["outcome"] = "dispatched"
		d["coordination"] = "windows-session-mutex"
		d["verification"] = map[string]any{"status": "pending"}
		d["input"] = map[string]any{"x": -10, "y": 20, "coordinate_space": "guest-screen-physical-pixels"}
		p = map[string]any{"observation": o, "target": map[string]any{"x": 0, "y": 0}}
	}
	payload["data"] = d
	payload["meta"].(map[string]any)["action"] = action
	payload["meta"].(map[string]any)["read_only"] = action != "ui.click"
	return payload, p, pixels
}
func TestVisualMCPNativeImageAndMetadataHaveNoDuplicateBase64(t *testing.T) {
	for _, action := range []string{"ui.observe", "ui.click"} {
		t.Run(action, func(t *testing.T) {
			payload, p, pixels := visualMCPFixture(t, action)
			before, _ := json.Marshal(payload)
			fixture := &developerFixture{invoke: func(context.Context, string, string, map[string]any, bool) (bridge.Result, error) {
				return payload, nil
			}}
			session, ctx := connectDeveloper(t, fixture, ServerOptions{DeveloperTools: true, AllowGuestWrite: action == "ui.click"})
			result := call(t, session, ctx, "dev."+action, devArgs(accessUUIDA, p))
			if result.IsError || len(result.Content) != 2 {
				t.Fatalf("visual native image failed: %#v", result)
			}
			image, ok := result.Content[1].(*mcp.ImageContent)
			if !ok || image.MIMEType != "image/png" || !bytes.Equal(image.Data, pixels) {
				t.Fatal("missing native PNG")
			}
			text := result.Content[0].(*mcp.TextContent).Text
			structured, _ := json.Marshal(result.StructuredContent)
			for _, metadata := range []string{text, string(structured)} {
				if strings.Contains(metadata, "png_base64") || strings.Contains(metadata, base64.StdEncoding.EncodeToString(pixels)) {
					t.Fatal("metadata duplicated pixels")
				}
			}
			var a, b any
			if json.Unmarshal([]byte(text), &a) != nil || json.Unmarshal(structured, &b) != nil || !reflect.DeepEqual(a, b) {
				t.Fatal("text and structured metadata differ")
			}
			after, _ := json.Marshal(payload)
			if !bytes.Equal(before, after) {
				t.Fatal("image handler mutated backend result")
			}
			if fixture.calls != 1 || fixture.write != (action == "ui.click") {
				t.Fatal("visual handler bypassed invocation authorization")
			}
		})
	}
}

func TestVisualMCPNestedGrantsAreCheckedBeforeBackendOrInventory(t *testing.T) {
	for _, test := range []struct {
		name, action        string
		grants              []string
		focus, noUIA, allow bool
	}{
		{"observe missing screenshot", "ui.observe", nil, false, false, false},
		{"observe missing UIA", "ui.observe", []string{"dev.screenshot.capture"}, false, false, false},
		{"visual only", "ui.observe", []string{"dev.screenshot.capture"}, false, true, true},
		{"observe all", "ui.observe", []string{"dev.screenshot.capture", "dev.ui.query"}, false, false, true},
		{"click missing input", "ui.click", []string{"dev.screenshot.capture", "dev.ui.query"}, false, false, false},
		{"click missing focus", "ui.click", []string{"dev.screenshot.capture", "dev.ui.query", "dev.input.mouse"}, true, false, false},
		{"click missing UIA", "ui.click", []string{"dev.screenshot.capture", "dev.input.mouse", "dev.window.focus"}, true, false, false},
		{"click all", "ui.click", []string{"dev.screenshot.capture", "dev.ui.query", "dev.input.mouse", "dev.window.focus"}, true, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload, p, _ := visualMCPFixture(t, test.action)
			p["focus"] = test.focus
			if test.action == "ui.observe" {
				delete(p, "focus")
			}
			if test.noUIA {
				p["include_uia"] = false
				payload["data"].(map[string]any)["uia"] = map[string]any{"status": "not_requested", "complete": false}
			}
			grants := map[string]bool{"dev." + test.action: true}
			for _, grant := range test.grants {
				grants[grant] = true
			}
			f := &developerFixture{invoke: func(context.Context, string, string, map[string]any, bool) (bridge.Result, error) {
				return payload, nil
			}}
			options := ServerOptions{DeveloperTools: true, AllowGuestWrite: true, AccessPolicy: &AccessPolicy{grants: map[string]map[string]bool{accessUUIDA: grants}}}
			session, ctx := connectDeveloper(t, f, options)
			result := call(t, session, ctx, "dev."+test.action, devArgs(accessUUIDA, p))
			if test.allow {
				if result.IsError || f.calls != 1 {
					t.Fatalf("granted flow rejected: %#v", result)
				}
			} else {
				requireCode(t, envelope(t, result), "ACCESS_DENIED")
				if f.calls != 0 || f.inventories != 0 {
					t.Fatal("denial reached backend or inventory")
				}
			}
		})
	}
}

func TestVisualMCPErrorsAreNotImagesAndClicksAreNotRetried(t *testing.T) {
	_, p, _ := visualMCPFixture(t, "ui.click")
	f := &developerFixture{invoke: func(context.Context, string, string, map[string]any, bool) (bridge.Result, error) {
		return bridge.Failure(bridge.NewError("ACTION_TIMEOUT", "Synthetic unknown outcome.", map[string]any{"outcome": "unknown"})), nil
	}}
	session, ctx := connectDeveloper(t, f, ServerOptions{DeveloperTools: true, AllowGuestWrite: true})
	result := call(t, session, ctx, "dev.ui.click", devArgs(accessUUIDA, p))
	requireCode(t, envelope(t, result), "ACTION_TIMEOUT")
	if len(result.Content) != 1 || f.calls != 1 {
		t.Fatal("unknown mutation retried or emitted an image")
	}
	metadata := envelope(t, result)
	if metadata["error"].(map[string]any)["details"].(map[string]any)["outcome"] != "unknown" {
		t.Fatal("unknown outcome lost")
	}
	bad := map[string]any{}
	for k, v := range p {
		bad[k] = v
	}
	bad["target"] = map[string]any{"x": -1, "y": 0}
	result = call(t, session, ctx, "dev.ui.click", devArgs(accessUUIDA, bad))
	requireCode(t, envelope(t, result), "INVALID_ARGUMENT")
	if f.calls != 1 {
		t.Fatal("invalid target called backend")
	}
}

func TestVisualMCPRejectsInvalidOptionalBackendAndKeepsOfflinePreview(t *testing.T) {
	for _, change := range []struct {
		name string
		fn   func(map[string]any)
	}{
		{"missing image", func(d map[string]any) { delete(d, "screenshot") }},
		{"invalid png", func(d map[string]any) { d["screenshot"].(map[string]any)["png_base64"] = "!!" }},
		{"preview containing image", func(d map[string]any) { d["dry_run"] = true }},
		{"changed token metadata", func(d map[string]any) { d["observation"].(map[string]any)["dpi"] = 0 }},
		{"extra pixels", func(d map[string]any) { d["png_base64"] = "never duplicated" }},
	} {
		for _, action := range []string{"ui.observe", "ui.click"} {
			t.Run(action+"/"+change.name, func(t *testing.T) {
				payload, p, _ := visualMCPFixture(t, action)
				// Response corruption must not also alter the caller's observation
				// through the fixture's intentionally shared original metadata.
				encoded, _ := json.Marshal(payload)
				decoder := json.NewDecoder(bytes.NewReader(encoded))
				decoder.UseNumber()
				if err := decoder.Decode(&payload); err != nil {
					t.Fatal(err)
				}
				change.fn(payload["data"].(map[string]any))
				fixture := &developerFixture{invoke: func(context.Context, string, string, map[string]any, bool) (bridge.Result, error) {
					return payload, nil
				}}
				session, ctx := connectDeveloper(t, fixture, ServerOptions{DeveloperTools: true, AllowGuestWrite: action == "ui.click"})
				result := call(t, session, ctx, "dev."+action, devArgs(accessUUIDA, p))
				metadata := envelope(t, result)
				requireCode(t, metadata, "INVALID_GUEST_RESPONSE")
				failure := metadata["error"].(map[string]any)
				if action == "ui.click" {
					if failure["details"].(map[string]any)["outcome"] != "unknown" {
						t.Fatal("optional backend validation lost submitted click uncertainty")
					}
				} else if failure["details"] != nil {
					t.Fatal("read-only validation gained mutation outcome")
				}
				if len(result.Content) != 1 || fixture.calls != 1 {
					t.Fatal("invalid response emitted image or retried backend")
				}
			})
		}
	}
	preview := bridge.Success(map[string]any{"dry_run": true, "argv": []string{"prlctl", "exec", "fixture-vm"}, "mutation": true})
	result := developerVisualImageResult(preview, map[string]any{}, "ui.click")
	if result.IsError || len(result.Content) != 1 {
		t.Fatal("offline preview became an image response")
	}
}

func TestVisualDynamicWorkflowFocusCannotOmitNestedGrant(t *testing.T) {
	workspace, err := newDeveloperWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	params := map[string]any{"focus": map[string]any{"$ref": "previous.data.focus"}, "include_uia": false}
	permissions, err := workspace.validate(ServerOptions{DeveloperTools: true, AllowGuestWrite: true}, "ui.click", params, accessUUIDA, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, permission := range permissions {
		found = found || permission == "dev.window.focus"
	}
	if !found {
		t.Fatal("unresolved workflow focus bypassed its nested grant")
	}
}
