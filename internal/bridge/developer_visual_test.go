package bridge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func visualFixture(t *testing.T, action string) map[string]any {
	t.Helper()
	s := screenshotFixtureData(t, 16, 8)
	o := map[string]any{"token": base64.StdEncoding.EncodeToString(make([]byte, 64)), "session_id": 1, "dpi": 192,
		"expires_at": "2026-01-02T03:05:30.1Z", "window_rect": map[string]any{"x": -108, "y": 17, "width": 32, "height": 24}}
	for _, key := range []string{"hwnd", "pid", "start_time_utc", "timestamp", "rect", "width", "height", "sha256", "coordinate_space", "capture_method"} {
		o[key] = s[key]
	}
	d := map[string]any{"action": action, "observation": o, "screenshot": s, "source": "visual", "uia": map[string]any{"status": "unavailable", "complete": false, "error_code": "UIA_TIMEOUT"}}
	if action == "ui.click" {
		d["outcome"] = "dispatched"
		d["coordination"] = "windows-session-mutex"
		d["verification"] = map[string]any{"status": "pending"}
		d["input"] = map[string]any{"x": -91, "y": 30, "coordinate_space": "guest-screen-physical-pixels"}
	}
	return d
}
func visualClickFixture(t *testing.T) map[string]any {
	return map[string]any{"observation": visualFixture(t, "ui.observe")["observation"], "target": map[string]any{"x": 4, "y": 2, "image_width": 8, "image_height": 4}}
}

func TestVisualRejectsInvalidInputAndWriteDenialWithoutTransport(t *testing.T) {
	for _, action := range []string{"ui.observe", "ui.click"} {
		for _, change := range []struct {
			name string
			fn   func(map[string]any)
		}{
			{"unknown", func(p map[string]any) { p["script"] = "never.ps1" }},
			{"timeout", func(p map[string]any) { p["timeout_ms"] = 0 }},
			{"uia flag", func(p map[string]any) { p["include_uia"] = "true" }},
			{"pixels", func(p map[string]any) { p["max_pixels"] = 0 }},
		} {
			t.Run(action+"/"+change.name, func(t *testing.T) {
				p := uiFixtureParams()
				if action == "ui.click" {
					p = visualClickFixture(t)
				}
				change.fn(p)
				f := newFixture(t)
				for _, dry := range []bool{false, true} {
					f.bridge.options.DryRun = dry
					_, err := f.bridge.Developer(context.Background(), "fixture-vm", action, p, true)
					checkCode(t, err, "INVALID_ARGUMENT")
				}
				if f.calls != 0 {
					t.Fatal("invalid input reached transport")
				}
			})
		}
	}
	for _, change := range []struct {
		name string
		fn   func(map[string]any)
	}{
		{"missing observation", func(p map[string]any) { delete(p, "observation") }},
		{"bad token", func(p map[string]any) { p["observation"].(map[string]any)["token"] = "not a token" }},
		{"logical space", func(p map[string]any) { p["observation"].(map[string]any)["coordinate_space"] = "logical" }},
		{"inconsistent dimensions", func(p map[string]any) { p["observation"].(map[string]any)["width"] = 17 }},
		{"wrong lifetime", func(p map[string]any) { p["observation"].(map[string]any)["expires_at"] = "2026-01-02T03:06:30.1Z" }},
		{"point outside", func(p map[string]any) { p["target"].(map[string]any)["x"] = 8 }},
		{"negative image point", func(p map[string]any) { p["target"].(map[string]any)["x"] = -1 }},
		{"partial rectangle", func(p map[string]any) { p["target"].(map[string]any)["width"] = 1 }},
		{"rectangle outside", func(p map[string]any) {
			p["target"].(map[string]any)["width"] = 5
			p["target"].(map[string]any)["height"] = 1
		}},
		{"ambiguous candidates", func(p map[string]any) { p["target"] = []any{p["target"], p["target"]} }},
		{"nonboolean focus", func(p map[string]any) { p["focus"] = "yes" }},
		{"unbounded age", func(p map[string]any) { p["max_age_ms"] = 30001 }},
	} {
		t.Run(change.name, func(t *testing.T) {
			p := visualClickFixture(t)
			change.fn(p)
			f := newFixture(t)
			_, err := f.bridge.Developer(context.Background(), "vm", "ui.click", p, true)
			checkCode(t, err, "INVALID_ARGUMENT")
			if f.calls != 0 {
				t.Fatal("invalid click reached transport")
			}
		})
	}
	f := newFixture(t)
	_, err := f.bridge.Developer(context.Background(), "vm", "ui.click", visualClickFixture(t), false)
	checkCode(t, err, "WRITE_NOT_ALLOWED")
	if f.calls != 0 {
		t.Fatal("unauthorized click reached transport")
	}
}

func TestVisualObservationAndClickUseOneTransportWithoutRetry(t *testing.T) {
	for _, action := range []string{"ui.observe", "ui.click"} {
		t.Run(action, func(t *testing.T) {
			p := uiFixtureParams()
			if action == "ui.click" {
				p = visualClickFixture(t)
			}
			before, _ := json.Marshal(p)
			normalized, err := normalizeDeveloperVisual(action, p)
			if err != nil {
				t.Fatal(err)
			}
			again, err := normalizeDeveloperVisual(action, normalized)
			if err != nil || !reflect.DeepEqual(again, normalized) {
				t.Fatal("normalization is not idempotent", err)
			}
			f := newFixture(t)
			f.response.stdout = developerResponse(action, action == "ui.click", visualFixture(t, action))
			result, err := f.bridge.Developer(context.Background(), "vm", action, p, action == "ui.click")
			if err != nil || result["ok"] != true || f.calls != 1 {
				t.Fatalf("visual flow failed: %v", err)
			}
			after, _ := json.Marshal(p)
			if string(before) != string(after) {
				t.Fatal("normalization mutated caller")
			}
			if action == "ui.click" && mapValue(t, mapValue(t, result["data"])["verification"])["status"] != "pending" {
				t.Fatal("dispatch claimed business success")
			}
			f = newFixture(t)
			f.bridge.options.DryRun = true
			if _, err = f.bridge.Developer(context.Background(), "vm", action, p, action == "ui.click"); err != nil || f.calls != 0 {
				t.Fatal("preview executed work", err)
			}
		})
	}
}

func TestVisualPrintWindowExplicitAndInheritedOnBaselineScreenshotContract(t *testing.T) {
	for _, action := range []string{"ui.observe", "ui.click"} {
		for _, explicit := range []bool{false, true} {
			p := uiFixtureParams()
			if action == "ui.click" {
				p = visualClickFixture(t)
			}
			if explicit {
				p["capture_method"] = "print_window"
			}
			normalized, err := normalizeDeveloperVisual(action, p)
			if err != nil || normalized["capture_method"] != "print_window" {
				t.Fatalf("%s explicit=%t: %v", action, explicit, err)
			}
			f := newFixture(t)
			f.response.stdout = developerResponse(action, action == "ui.click", visualFixture(t, action))
			if _, err = f.bridge.Developer(context.Background(), "vm", action, p, action == "ui.click"); err != nil || f.calls != 1 {
				t.Fatalf("print_window flow failed: %v", err)
			}
		}
	}
}

func TestVisualPhysicalMappingUsesCaptureBoundaryAndImageGrid(t *testing.T) {
	o := visualFixture(t, "ui.observe")["observation"].(map[string]any)
	for _, test := range []struct {
		target map[string]any
		want   [2]int64
	}{
		{map[string]any{"x": 0, "y": 0, "image_width": 16, "image_height": 8}, [2]int64{-100, 25}},
		{map[string]any{"x": 7, "y": 3, "image_width": 8, "image_height": 4}, [2]int64{-85, 32}},
		{map[string]any{"x": 4, "y": 2, "width": 2, "height": 2, "image_width": 8, "image_height": 4}, [2]int64{-90, 31}},
	} {
		p, ok := visualPhysicalPoint(o, test.target)
		if !ok || p != test.want {
			t.Fatalf("mapped %v, want %v", p, test.want)
		}
	}
}

func TestVisualUIAAvailableAndIncompleteAreExplicitAndBounded(t *testing.T) {
	for _, complete := range []bool{true, false} {
		params := uiFixtureParams()
		params["limit"] = 64
		params["max_depth"] = 6
		params["max_scan_nodes"] = 500
		queryParams, err := normalizeDeveloperUIQuery(params)
		if err != nil {
			t.Fatal(err)
		}
		query := uiFixtureData(queryParams)
		query["complete"] = complete
		d := visualFixture(t, "ui.observe")
		d["source"] = "uia-and-visual"
		d["uia"] = map[string]any{"status": "available", "complete": complete, "data": query}
		f := newFixture(t)
		f.response.stdout = developerResponse("ui.observe", false, d)
		if _, err = f.bridge.Developer(context.Background(), "vm", "ui.observe", uiFixtureParams(), false); err != nil {
			t.Fatalf("bounded UIA rejected: %v", err)
		}
		query["limit"] = 200
		f.response.stdout = developerResponse("ui.observe", false, d)
		_, err = f.bridge.Developer(context.Background(), "vm", "ui.observe", uiFixtureParams(), false)
		checkCode(t, err, "INVALID_GUEST_RESPONSE")
	}
}

func TestVisualRejectsInvalidGuestObservationAndUnconfirmedPostImage(t *testing.T) {
	for _, test := range []struct {
		name string
		fn   func(map[string]any)
	}{
		{"wrong session", func(d map[string]any) { d["observation"].(map[string]any)["session_id"] = 2 }},
		{"wrong hash", func(d map[string]any) { d["observation"].(map[string]any)["sha256"] = "bad" }},
		{"hidden base64", func(d map[string]any) { d["png_base64"] = "bad" }},
		{"mismatched image", func(d map[string]any) { d["screenshot"].(map[string]any)["pid"] = 43 }},
		{"false verification", func(d map[string]any) { d["verification"] = map[string]any{"status": "verified"} }},
		{"wrong mapped point", func(d map[string]any) { d["input"].(map[string]any)["x"] = -92 }},
		{"hidden uia data", func(d map[string]any) { d["uia"].(map[string]any)["data"] = map[string]any{"private": "fixture"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			d := visualFixture(t, "ui.click")
			test.fn(d)
			f.response.stdout = developerResponse("ui.click", true, d)
			_, err := f.bridge.Developer(context.Background(), "vm", "ui.click", visualClickFixture(t), true)
			failure := checkCode(t, err, "INVALID_GUEST_RESPONSE")
			if failure.Details["outcome"] != "unknown" {
				t.Fatal("post-click validation lost unknown outcome")
			}
			if f.calls != 1 {
				t.Fatal("click retried")
			}
		})
	}
	f := newFixture(t)
	f.err = context.DeadlineExceeded
	_, err := f.bridge.Developer(context.Background(), "vm", "ui.click", visualClickFixture(t), true)
	var failure *Error
	if !errors.As(err, &failure) || failure.Details["outcome"] != "unknown" || f.calls != 1 {
		t.Fatal("transport timeout lost unknown outcome", err)
	}
}

func TestVisualResponseMetadataFailuresPreserveMutationUncertainty(t *testing.T) {
	for _, action := range []string{"ui.observe", "ui.click"} {
		for _, test := range []struct {
			name   string
			change func(Result)
		}{
			{"missing meta", func(r Result) { delete(r, "meta") }},
			{"protocol", func(r Result) { r["meta"].(map[string]any)["protocol_version"] = "unsupported" }},
			{"action", func(r Result) { r["meta"].(map[string]any)["action"] = "other.action" }},
			{"session", func(r Result) { r["meta"].(map[string]any)["session_id"] = 2 }},
			{"noninteractive", func(r Result) { r["meta"].(map[string]any)["user_interactive"] = false }},
			{"invalid envelope", func(r Result) { r["ok"] = "true" }},
		} {
			t.Run(action+"/"+test.name, func(t *testing.T) {
				p := uiFixtureParams()
				if action == "ui.click" {
					p = visualClickFixture(t)
				}
				value, _ := decodeJSON(developerResponse(action, action == "ui.click", visualFixture(t, action)))
				response, _ := asObject(value)
				test.change(Result(response))
				encoded, _ := json.Marshal(response)
				f := newFixture(t)
				f.response.stdout = string(encoded)
				_, err := f.bridge.Developer(context.Background(), "vm", action, p, action == "ui.click")
				failure := checkCode(t, err, "INVALID_GUEST_RESPONSE")
				if action == "ui.click" {
					if failure.Details["outcome"] != "unknown" {
						t.Fatal("submitted click lost unknown outcome")
					}
				} else if failure.Details["outcome"] != nil {
					t.Fatal("read-only observation gained mutation outcome")
				}
				if f.calls != 1 {
					t.Fatal("validation retried transport")
				}
			})
		}
	}
	for _, test := range []struct {
		name, response, code string
		exit                 int
	}{
		{"invalid JSON", "synthetic invalid JSON", "INVALID_JSON", 0},
		{"exit mismatch", developerResponse("ui.click", true, visualFixture(t, "ui.click")), "GUEST_EXIT_MISMATCH", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			f.response.stdout = test.response
			f.response.exitCode = test.exit
			_, err := f.bridge.Developer(context.Background(), "vm", "ui.click", visualClickFixture(t), true)
			failure := checkCode(t, err, test.code)
			if failure.Details["outcome"] != "unknown" || f.calls != 1 {
				t.Fatal("submitted response failure lost uncertainty or retried")
			}
		})
	}
	for _, dry := range []bool{false, true} {
		f := newFixture(t)
		f.bridge.options.DryRun = dry
		_, err := f.bridge.Developer(context.Background(), "vm", "ui.click", visualClickFixture(t), false)
		failure := checkCode(t, err, "WRITE_NOT_ALLOWED")
		if failure.Details["outcome"] != nil || f.calls != 0 {
			t.Fatal("preflight rejection treated as submitted input")
		}
	}
}
