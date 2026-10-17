package bridge

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"image/png"
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
}

func normalizeDeveloperScreenshot(input map[string]any) (map[string]any, error) {
	if err := uiQueryFields(input, "hwnd", "pid", "start_time_utc", "timeout_ms", "max_bytes", "max_pixels"); err != nil {
		return nil, err
	}
	result, err := normalizeDeveloperUIWindow(input)
	if err != nil {
		return nil, err
	}
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
		data["coordinate_space"] != "guest-screen-physical-pixels" || data["capture_method"] != "print_window" {
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
	if !warningsOK || len(warnings) != 1 || warnings[0] != "application-rendered-best-effort" {
		return false
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
	return pid == wantPID && length <= maxBytes && width*height <= maxPixels
}
