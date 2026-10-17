package bridge

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"image/png"
	"slices"
	"time"
)

const developerScreenshotMaxBytes = 4 * 1024 * 1024
const developerScreenshotMaxPixels = 16 * 1024 * 1024

func init() {
	registerDeveloperAction(DeveloperAction{
		Name: "screenshot.capture", Description: "Request a bounded PNG rendering of an identified Windows window without activating it.",
		Interactive: true, Normalize: normalizeDeveloperScreenshot,
		ValidateResult: validDeveloperScreenshot, ValidateWithinRequest: developerScreenshotWithinRequest,
	})
	registerDeveloperAction(DeveloperAction{
		Name: "screenshot.capabilities", Description: "Probe screenshot OS, API and hardware support without capturing a window; session readiness is reported separately.",
		Normalize: normalizeDeveloperScreenshotCapabilities, ValidateResult: validDeveloperScreenshotCapabilities,
	})
}

func normalizeDeveloperScreenshot(input map[string]any) (map[string]any, error) {
	if err := uiQueryFields(input, "hwnd", "pid", "start_time_utc", "timeout_ms", "max_bytes", "max_pixels", "capture_method"); err != nil {
		return nil, err
	}
	result, err := normalizeDeveloperUIWindow(input)
	if err != nil {
		return nil, err
	}
	method, err := uiQueryString(input, "capture_method", "print_window", 32)
	if err != nil {
		return nil, err
	}
	if method != "print_window" && method != "windows_graphics_capture" {
		return nil, uiQueryArgument("capture_method must be print_window or windows_graphics_capture")
	}
	result["capture_method"] = method
	for _, bound := range []struct {
		key                string
		fallback, min, max int64
	}{{"timeout_ms", 10000, 1000, 20000}, {"max_bytes", developerScreenshotMaxBytes, 1024, developerScreenshotMaxBytes}, {"max_pixels", 8 * 1024 * 1024, 1, developerScreenshotMaxPixels}} {
		result[bound.key], err = uiQueryInteger(input, bound.key, bound.fallback, bound.min, bound.max)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func validDeveloperScreenshot(data map[string]any) bool {
	if uiQueryFields(data, "hwnd", "pid", "start_time_utc", "timestamp", "capture_started_at", "coordinate_space", "rect", "width", "height", "png_base64", "sha256", "byte_length", "capture_method", "warnings") != nil {
		return false
	}
	if _, err := normalizeDeveloperUIWindow(data); err != nil {
		return false
	}
	stamp, stampOK := data["timestamp"].(string)
	started, startedOK := data["capture_started_at"].(string)
	finishedAt, stampErr := time.Parse(time.RFC3339Nano, stamp)
	startedAt, startedErr := time.Parse(time.RFC3339Nano, started)
	if !stampOK || !startedOK || stampErr != nil || startedErr != nil || finishedAt.Before(startedAt) ||
		finishedAt.Sub(startedAt) > 20*time.Second ||
		data["coordinate_space"] != "guest-screen-physical-pixels" {
		return false
	}
	width, wOK := integral(data["width"])
	height, hOK := integral(data["height"])
	if !wOK || !hOK || width < 1 || height < 1 || width > 16384 || height > 16384 || width*height > developerScreenshotMaxPixels {
		return false
	}
	rect, rectOK := asObject(data["rect"])
	if !rectOK || uiQueryFields(rect, "x", "y", "width", "height") != nil ||
		!responseInteger(rect["x"], -2147483648, 2147483647) || !responseInteger(rect["y"], -2147483648, 2147483647) {
		return false
	}
	rectWidth, _ := integral(rect["width"])
	rectHeight, _ := integral(rect["height"])
	if rectWidth != width || rectHeight != height {
		return false
	}
	warnings, warningsOK := data["warnings"].([]any)
	expectedWarnings := []string{"application-rendered-best-effort"}
	switch data["capture_method"] {
	case "print_window":
	case "windows_graphics_capture":
		expectedWarnings = []string{"compositor-frame-best-effort", "bgra8-hdr-may-clip", "system-capture-border-may-appear"}
	default:
		return false
	}
	if !warningsOK || len(warnings) != len(expectedWarnings) {
		return false
	}
	for index, expected := range expectedWarnings {
		if warnings[index] != expected {
			return false
		}
	}
	encoded, encodedOK := data["png_base64"].(string)
	if !encodedOK || len(encoded) > base64.StdEncoding.EncodedLen(developerScreenshotMaxBytes) {
		return false
	}
	imageBytes, err := base64.StdEncoding.Strict().DecodeString(encoded)
	length, lengthOK := integral(data["byte_length"])
	if err != nil || !lengthOK || length != int64(len(imageBytes)) || length < 1 || length > developerScreenshotMaxBytes {
		return false
	}
	hash, hashOK := data["sha256"].(string)
	digest := sha256.Sum256(imageBytes)
	if !hashOK || hash != hex.EncodeToString(digest[:]) {
		return false
	}
	config, err := png.DecodeConfig(bytes.NewReader(imageBytes))
	if err != nil || int64(config.Width) != width || int64(config.Height) != height {
		return false
	}
	// The dimensions and compressed bytes are bounded before decompression. Decode
	// the complete image so a valid IHDR alone cannot masquerade as evidence.
	_, err = png.Decode(bytes.NewReader(imageBytes))
	return err == nil
}

func developerScreenshotWithinRequest(data, params map[string]any) bool {
	if data["capture_method"] != params["capture_method"] {
		return false
	}
	for _, key := range []string{"hwnd", "start_time_utc"} {
		if data[key] != params[key] {
			return false
		}
	}
	pid, _ := integral(data["pid"])
	wantPID, _ := integral(params["pid"])
	length, _ := integral(data["byte_length"])
	maxBytes, _ := integral(params["max_bytes"])
	width, _ := integral(data["width"])
	height, _ := integral(data["height"])
	maxPixels, _ := integral(params["max_pixels"])
	timeout, _ := integral(params["timeout_ms"])
	startedText, _ := data["capture_started_at"].(string)
	finishedText, _ := data["timestamp"].(string)
	started, startedErr := time.Parse(time.RFC3339Nano, startedText)
	finished, finishedErr := time.Parse(time.RFC3339Nano, finishedText)
	return pid == wantPID && length <= maxBytes && width*height <= maxPixels && timeout >= 1000 &&
		startedErr == nil && finishedErr == nil && !finished.Before(started) && finished.Sub(started) <= time.Duration(timeout)*time.Millisecond
}

func normalizeDeveloperScreenshotCapabilities(input map[string]any) (map[string]any, error) {
	if err := uiQueryFields(input, "timeout_ms"); err != nil {
		return nil, err
	}
	timeout, err := uiQueryInteger(input, "timeout_ms", 10000, 1000, 20000)
	if err != nil {
		return nil, err
	}
	return map[string]any{"timeout_ms": timeout}, nil
}

func validDeveloperScreenshotCapabilities(data map[string]any) bool {
	if uiQueryFields(data, "probe_scope", "os_build", "session_id", "user_interactive", "capture_requires_input_desktop", "methods") != nil ||
		data["probe_scope"] != "os-api-device" || !responseInteger(data["os_build"], 0, 2147483647) ||
		!responseInteger(data["session_id"], 0, 2147483647) || data["capture_requires_input_desktop"] != true {
		return false
	}
	if _, ok := data["user_interactive"].(bool); !ok {
		return false
	}
	methods, ok := data["methods"].([]any)
	if !ok || len(methods) != 2 {
		return false
	}
	for index, name := range []string{"print_window", "windows_graphics_capture"} {
		method, ok := asObject(methods[index])
		if !ok || uiQueryFields(method, "capture_method", "available", "reason") != nil || method["capture_method"] != name {
			return false
		}
		available, ok := method["available"].(bool)
		if !ok {
			return false
		}
		if available {
			if method["reason"] != "available" {
				return false
			}
		} else {
			reason, ok := method["reason"].(string)
			if !ok || name == "print_window" || !slices.Contains([]string{"WGC_OS_UNSUPPORTED", "WGC_API_UNAVAILABLE", "WGC_UNSUPPORTED", "WGC_ACCESS_DENIED", "WGC_DEVICE_UNAVAILABLE", "WGC_DEVICE_LOST"}, reason) {
				return false
			}
		}
	}
	return true
}
