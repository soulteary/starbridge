package bridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestEvidenceScopesObservationsAndRejectsWrites(t *testing.T) {
	for _, action := range []string{"processes.list", "windows.list", "task.start", "workflow.run", "vm.start"} {
		_, err := normalizeEvidence(map[string]any{"output_directory": t.TempDir(), "observations": []any{map[string]any{"name": "synthetic", "action": action}}})
		if err == nil {
			t.Errorf("accepted broad or mutating observation %s", action)
		}
	}
	_, err := normalizeEvidence(map[string]any{"output_directory": t.TempDir(), "observations": []any{map[string]any{"name": "../escape", "action": "system.info"}}})
	if err == nil {
		t.Fatal("accepted artifact path traversal")
	}
}

func TestEvidenceCapturesSyntheticProcessAndHashes(t *testing.T) {
	f := newFixture(t)
	f.response.stdout = `{"ok":true,"data":{"processes":[{"pid":42,"name":"SyntheticFixture"}],"limit":1,"truncated":false},"meta":{"protocol_version":"1","action":"processes.list","session_id":1,"user_interactive":true}}`
	p, err := normalizeEvidence(map[string]any{"output_directory": t.TempDir(), "observations": []any{map[string]any{"name": "synthetic", "action": "processes.list", "params": map[string]any{"pid": 42, "limit": 1}}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.bridge.captureEvidence(context.Background(), "11111111-1111-4111-8111-111111111111", p)
	if err != nil {
		t.Fatal(err)
	}
	data := mapValue(t, result["data"])
	if data["complete"] != true || f.calls != 1 {
		t.Fatal("capture did not complete")
	}
	manifestPath := data["manifest_file"].(string)
	encoded, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if json.Unmarshal(encoded, &manifest) != nil {
		t.Fatal("invalid manifest")
	}
	observations := manifest["observations"].([]any)
	record := observations[0].(map[string]any)
	files := record["files"].([]any)
	for _, item := range files {
		entry := item.(map[string]any)
		content, err := os.ReadFile(filepath.Join(filepath.Dir(manifestPath), entry["name"].(string)))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(content)
		if entry["sha256"] != hex.EncodeToString(sum[:]) {
			t.Fatal("artifact hash mismatch")
		}
	}
}

func TestEvidenceImageAndBinaryLogAreSeparateArtifacts(t *testing.T) {
	directory := t.TempDir()
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	result := Success(map[string]any{"png_base64": base64.StdEncoding.EncodeToString(imageBytes.Bytes()), "width": 2, "height": 2})
	files, err := evidencePayload(directory, "synthetic-image", "screenshot.capture", result)
	if err != nil || len(files) != 2 {
		t.Fatalf("image artifacts=%v err=%v", files, err)
	}
	if _, exists := mapValue(t, result["data"])["png_base64"]; !exists {
		t.Fatal("capture mutated caller map")
	}
	output := []byte{'x', 0, 255, '\n'}
	_, err = evidencePayload(directory, "synthetic-log", "task.output", Success(map[string]any{"data_base64": base64.StdEncoding.EncodeToString(output)}))
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(filepath.Join(directory, "synthetic-log.log"))
	if err != nil || !bytes.Equal(output, actual) {
		t.Fatal("binary log was corrupted")
	}
	if _, err = evidencePayload(directory, "invalid-image", "screenshot.capture", Success(map[string]any{"png_base64": "not-base64"})); err == nil {
		t.Fatal("accepted invalid image")
	}
}

func TestEvidencePreservesFailuresAsPartialManifest(t *testing.T) {
	f := newFixture(t)
	f.response = commandResult{exitCode: 7, stderr: "Synthetic failure"}
	p, err := normalizeEvidence(map[string]any{"output_directory": t.TempDir(), "observations": []any{map[string]any{"name": "synthetic", "action": "processes.list", "params": map[string]any{"pid": 42, "limit": 1}}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := f.bridge.captureEvidence(context.Background(), "11111111-1111-4111-8111-111111111111", p)
	if err != nil || mapValue(t, result["data"])["complete"] != false {
		t.Fatal("failure reported complete evidence")
	}
}
