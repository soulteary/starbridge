package mcpserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/soulteary/starbridge/internal/bridge"
)

func developerImageFixture(t *testing.T) (bridge.Result, []byte) {
	t.Helper()
	bitmap := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	bitmap.SetNRGBA(0, 0, color.NRGBA{R: 30, G: 90, B: 160, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, bitmap); err != nil {
		t.Fatal(err)
	}
	data := map[string]any{
		"hwnd": "0x123", "pid": 42, "start_time_utc": "2026-01-02T03:04:05Z",
		"timestamp": "2026-01-02T03:05:00.1Z", "capture_started_at": "2026-01-02T03:05:00Z",
		"coordinate_space": "guest-screen-physical-pixels", "rect": map[string]any{"x": -10, "y": 20, "width": 1, "height": 1},
		"width": 1, "height": 1, "capture_method": "print_window", "warnings": []any{"application-rendered-best-effort"},
	}
	setDeveloperImageBytes(data, encoded.Bytes())
	return bridge.Result{"ok": true, "data": data, "meta": map[string]any{"protocol_version": "dev-1", "action": "screenshot.capture", "read_only": true, "session_id": 1, "user_interactive": true}}, encoded.Bytes()
}

func setDeveloperImageBytes(data map[string]any, imageBytes []byte) {
	digest := sha256.Sum256(imageBytes)
	data["png_base64"] = base64.StdEncoding.EncodeToString(imageBytes)
	data["byte_length"] = len(imageBytes)
	data["sha256"] = hex.EncodeToString(digest[:])
}

func developerImageOptions() ServerOptions {
	return ServerOptions{DeveloperTools: true, developerActions: []bridge.DeveloperAction{{
		Name: "screenshot.capture", Interactive: true,
		Normalize:      func(p map[string]any) (map[string]any, error) { return p, nil },
		ValidateResult: func(map[string]any) bool { return true },
	}}}
}

func callDeveloperImage(t *testing.T, payload bridge.Result, params map[string]any) *mcp.CallToolResult {
	t.Helper()
	fixture := &developerFixture{invoke: func(context.Context, string, string, map[string]any, bool) (bridge.Result, error) {
		return payload, nil
	}}
	session, ctx := connectDeveloper(t, fixture, developerImageOptions())
	result := call(t, session, ctx, "dev.screenshot.capture", devArgs(accessUUIDA, params))
	if fixture.calls != 1 || fixture.write {
		t.Fatal("image response bypassed the original read-only invocation")
	}
	return result
}

func TestDeveloperScreenshotUsesNativeMCPImageAndPrivateMetadata(t *testing.T) {
	payload, pngBytes := developerImageFixture(t)
	before, _ := json.Marshal(payload)
	result := callDeveloperImage(t, payload, map[string]any{})
	if result.IsError || len(result.Content) != 2 {
		t.Fatal("expected metadata and one native image")
	}
	text, textOK := result.Content[0].(*mcp.TextContent)
	imageContent, imageOK := result.Content[1].(*mcp.ImageContent)
	if !textOK || !imageOK || imageContent.MIMEType != "image/png" || !bytes.Equal(imageContent.Data, pngBytes) {
		t.Fatal("the real MCP SDK did not preserve raw PNG image content")
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(text.Text), &metadata); err != nil {
		t.Fatal(err)
	}
	structured, _ := json.Marshal(result.StructuredContent)
	var structuredMetadata map[string]any
	if json.Unmarshal(structured, &structuredMetadata) != nil || !reflect.DeepEqual(metadata, structuredMetadata) {
		t.Fatal("metadata text and structured content differ")
	}
	data := metadata["data"].(map[string]any)
	if _, exists := data["png_base64"]; exists || strings.Contains(text.Text, "png_base64") || strings.Contains(text.Text, base64.StdEncoding.EncodeToString(pngBytes)) {
		t.Fatal("image bytes were duplicated as metadata text")
	}
	for _, field := range []string{"timestamp", "capture_started_at", "coordinate_space", "sha256", "hwnd", "start_time_utc"} {
		if data[field] != payload["data"].(map[string]any)[field] {
			t.Fatal("image conversion dropped observation metadata")
		}
	}
	if data["width"] != float64(1) || data["height"] != float64(1) || metadata["meta"].(map[string]any)["action"] != "screenshot.capture" {
		t.Fatal("image dimensions or protocol metadata were lost")
	}
	after, _ := json.Marshal(payload)
	if !bytes.Equal(before, after) {
		t.Fatal("MCP image conversion mutated the Bridge response")
	}
}

func TestDeveloperScreenshotRejectsInvalidImagesFromOptionalBackend(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(map[string]any, []byte)
	}{
		{"missing image", func(data map[string]any, _ []byte) { delete(data, "png_base64") }},
		{"invalid base64", func(data map[string]any, _ []byte) { data["png_base64"] = "!!" }},
		{"noncanonical base64", func(data map[string]any, _ []byte) { data["png_base64"] = data["png_base64"].(string) + "\n" }},
		{"encoded budget", func(data map[string]any, _ []byte) {
			data["png_base64"] = strings.Repeat("A", base64.StdEncoding.EncodedLen(maxMCPImageBytes)+4)
		}},
		{"wrong hash", func(data map[string]any, _ []byte) { data["sha256"] = strings.Repeat("0", 64) }},
		{"wrong size", func(data map[string]any, _ []byte) { data["byte_length"] = 1 }},
		{"wrong width", func(data map[string]any, _ []byte) { data["width"] = 2 }},
		{"invalid dimension", func(data map[string]any, _ []byte) { data["height"] = 0 }},
		{"pixel budget", func(data map[string]any, _ []byte) { data["width"], data["height"] = 8192, 8192 }},
		{"only PNG header", func(data map[string]any, pngBytes []byte) { setDeveloperImageBytes(data, pngBytes[:33]) }},
		{"non PNG bytes", func(data map[string]any, _ []byte) { setDeveloperImageBytes(data, []byte("synthetic non-image")) }},
		{"preview with invalid image", func(data map[string]any, _ []byte) { data["dry_run"], data["png_base64"] = true, "!!" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			payload, pngBytes := developerImageFixture(t)
			test.change(payload["data"].(map[string]any), pngBytes)
			result := callDeveloperImage(t, payload, map[string]any{})
			requireCode(t, envelope(t, result), "INVALID_GUEST_RESPONSE")
			for _, content := range result.Content {
				if _, image := content.(*mcp.ImageContent); image {
					t.Fatal("invalid screenshot emitted an image")
				}
			}
		})
	}
}

func TestDeveloperScreenshotHonorsRequestedCompressedBudget(t *testing.T) {
	payload, pngBytes := developerImageFixture(t)
	// Keep a generated 1x1 PNG, adding a valid ancillary text chunk so its
	// compressed file is larger than the smallest accepted caller byte budget.
	text := append([]byte("fixture\x00"), bytes.Repeat([]byte{'x'}, 2048)...)
	chunk := make([]byte, len(text)+12)
	binary.BigEndian.PutUint32(chunk, uint32(len(text)))
	copy(chunk[4:8], "tEXt")
	copy(chunk[8:], text)
	binary.BigEndian.PutUint32(chunk[len(chunk)-4:], crc32.ChecksumIEEE(chunk[4:len(chunk)-4]))
	augmented := append(append(append([]byte{}, pngBytes[:len(pngBytes)-12]...), chunk...), pngBytes[len(pngBytes)-12:]...)
	if _, err := png.Decode(bytes.NewReader(augmented)); err != nil {
		t.Fatal("fixture is not a complete 1x1 PNG")
	}
	setDeveloperImageBytes(payload["data"].(map[string]any), augmented)
	result := callDeveloperImage(t, payload, map[string]any{"max_bytes": 1024, "max_pixels": 1})
	requireCode(t, envelope(t, result), "INVALID_GUEST_RESPONSE")
	result = callDeveloperImage(t, payload, map[string]any{"max_bytes": 4096, "max_pixels": 1})
	if result.IsError || len(result.Content) != 2 {
		t.Fatal("valid image within caller budgets was rejected")
	}
}

func TestDeveloperScreenshotErrorsAndOfflinePreviewRemainOrdinaryEnvelopes(t *testing.T) {
	for _, payload := range []bridge.Result{
		bridge.Failure(bridge.NewError("UIA_TIMEOUT", "Synthetic screenshot timeout.", nil)),
		bridge.Success(map[string]any{"dry_run": true, "argv": []string{"prlctl", "exec", "fixture-vm"}, "mutation": false}),
	} {
		result := callDeveloperImage(t, payload, map[string]any{})
		metadata := envelope(t, result)
		want, _ := json.Marshal(payload)
		got, _ := json.Marshal(metadata)
		if !bytes.Equal(want, got) {
			t.Fatal("ordinary failure or offline preview was changed into an image response")
		}
	}
}
