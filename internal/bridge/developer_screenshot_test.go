package bridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func screenshotFixtureData(t *testing.T, width, height int) map[string]any {
	t.Helper()
	bitmap := image.NewNRGBA(image.Rect(0, 0, width, height))
	state := uint32(123456789)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			state = state*1664525 + 1013904223
			bitmap.SetNRGBA(x, y, color.NRGBA{R: byte(state >> 16), G: byte(state >> 8), B: byte(state), A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, bitmap); err != nil {
		t.Fatal(err)
	}
	data := map[string]any{
		"hwnd": "0x123", "pid": 42, "start_time_utc": uiFixtureStart,
		"timestamp": "2026-01-02T03:05:00.1Z", "capture_started_at": "2026-01-02T03:05:00Z",
		"coordinate_space": "guest-screen-physical-pixels", "capture_method": "print_window",
		"rect": map[string]any{"x": -100, "y": 25, "width": width, "height": height}, "width": width, "height": height,
		"warnings": []any{"application-rendered-best-effort"},
	}
	setScreenshotFixtureBytes(data, encoded.Bytes())
	return data
}

func setScreenshotFixtureBytes(data map[string]any, imageBytes []byte) {
	hash := sha256.Sum256(imageBytes)
	data["png_base64"] = base64.StdEncoding.EncodeToString(imageBytes)
	data["sha256"] = hex.EncodeToString(hash[:])
	data["byte_length"] = len(imageBytes)
}

func screenshotFixtureResponse(t *testing.T, data map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(Result{"ok": true, "data": data, "meta": map[string]any{
		"protocol_version": DeveloperProtocol, "action": "screenshot.capture", "session_id": 1, "user_interactive": true, "read_only": true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func TestDeveloperScreenshotRejectsArgumentsBeforeTransport(t *testing.T) {
	for _, test := range []struct {
		key   string
		value any
	}{
		{"path", "fixture.png"}, {"hwnd", 123}, {"pid", 0}, {"start_time_utc", ""},
		{"max_bytes", 1023}, {"max_bytes", developerScreenshotMaxBytes + 1}, {"max_bytes", true},
		{"max_pixels", 0}, {"max_pixels", developerScreenshotMaxPixels + 1}, {"max_pixels", 1.5},
		{"timeout_ms", 999}, {"timeout_ms", 20001},
	} {
		t.Run(test.key+"/"+stringValueForTest(test.value), func(t *testing.T) {
			params := uiFixtureParams()
			params[test.key] = test.value
			f := newFixture(t)
			_, err := f.bridge.Developer(context.Background(), "fixture-vm", "screenshot.capture", params, false)
			checkCode(t, err, "INVALID_ARGUMENT")
			if f.calls != 0 {
				t.Fatal("invalid capture reached transport")
			}
		})
	}
}

func TestDeveloperScreenshotDryRunIsReadOnlyAndHasNoHostOutputPath(t *testing.T) {
	f := newFixture(t)
	f.bridge.options.DryRun = true
	result, err := f.bridge.Developer(context.Background(), "fixture-vm", "screenshot.capture", uiFixtureParams(), false)
	if err != nil {
		t.Fatal(err)
	}
	args := mapValue(t, result["data"])["argv"].([]string)
	if f.calls != 0 || args[len(args)-2] != "-RequestBase64" {
		t.Fatal("capture dry run performed work")
	}
	for _, argument := range args {
		if argument == "-AllowWrite" {
			t.Fatal("capture requested write permission")
		}
	}
	request, err := base64.StdEncoding.DecodeString(args[len(args)-1])
	if err != nil {
		t.Fatal(err)
	}
	value, err := decodeJSON(string(request))
	if err != nil {
		t.Fatal(err)
	}
	normalized := mapValue(t, mapValue(t, value)["params"])
	if normalized["hwnd"] != "0x123" || !responseInteger(normalized["max_bytes"], developerScreenshotMaxBytes, developerScreenshotMaxBytes) {
		t.Fatal("capture budgets were not normalized")
	}
}

func TestDeveloperScreenshotValidatesCompleteImageAndIdentity(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(map[string]any)
		valid  bool
	}{
		{"generated PNG", func(map[string]any) {}, true},
		{"wrong process", func(d map[string]any) { d["pid"] = 43 }, false},
		{"wrong window", func(d map[string]any) { d["hwnd"] = "0x124" }, false},
		{"reused PID", func(d map[string]any) { d["start_time_utc"] = "2026-01-02T03:04:06Z" }, false},
		{"bad hash", func(d map[string]any) { d["sha256"] = "bad" }, false},
		{"bad base64", func(d map[string]any) { d["png_base64"] = "!!" }, false},
		{"wrong byte length", func(d map[string]any) { d["byte_length"] = 1 }, false},
		{"wrong dimensions", func(d map[string]any) { d["width"] = 17 }, false},
		{"wrong rectangle", func(d map[string]any) { d["rect"].(map[string]any)["height"] = 9 }, false},
		{"time reversed", func(d map[string]any) { d["capture_started_at"] = "2026-01-02T03:06:00Z" }, false},
		{"logical coordinates", func(d map[string]any) { d["coordinate_space"] = "logical" }, false},
		{"silent best effort", func(d map[string]any) { d["warnings"] = []any{} }, false},
		{"unexpected private path", func(d map[string]any) { d["path"] = "fixture.png" }, false},
		{"non PNG content", func(d map[string]any) { setScreenshotFixtureBytes(d, []byte("synthetic bytes")) }, false},
		{"valid header without pixels", func(d map[string]any) {
			imageBytes, _ := base64.StdEncoding.DecodeString(d["png_base64"].(string))
			setScreenshotFixtureBytes(d, imageBytes[:33])
		}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			data := screenshotFixtureData(t, 16, 8)
			test.change(data)
			f := newFixture(t)
			f.response.stdout = screenshotFixtureResponse(t, data)
			result, err := f.bridge.Developer(context.Background(), "fixture-vm", "screenshot.capture", uiFixtureParams(), false)
			if test.valid {
				if err != nil || result["ok"] != true {
					t.Fatalf("capture was rejected: %v", err)
				}
			} else {
				checkCode(t, err, "INVALID_GUEST_RESPONSE")
			}
			if f.calls != 1 {
				t.Fatal("capture was retried")
			}
		})
	}
}

func TestDeveloperScreenshotHonorsCallerImageBudgets(t *testing.T) {
	for _, test := range []struct {
		name string
		key  string
		max  int
	}{{"pixel budget", "max_pixels", 1}, {"byte budget", "max_bytes", 1024}} {
		t.Run(test.name, func(t *testing.T) {
			params := uiFixtureParams()
			params[test.key] = test.max
			f := newFixture(t)
			f.response.stdout = screenshotFixtureResponse(t, screenshotFixtureData(t, 64, 64))
			_, err := f.bridge.Developer(context.Background(), "fixture-vm", "screenshot.capture", params, false)
			checkCode(t, err, "INVALID_GUEST_RESPONSE")
			if f.calls != 1 {
				t.Fatal("capture was retried")
			}
		})
	}
}
