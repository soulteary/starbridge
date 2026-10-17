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
	for _, dryRun := range []bool{false, true} {
		f := newFixture(t)
		f.bridge.options.DryRun = dryRun
		_, err := f.bridge.Developer(context.Background(), "11111111-1111-4111-8111-111111111111", "evidence.capture", map[string]any{
			"output_directory": t.TempDir(), "observations": []any{map[string]any{"name": "manifest", "action": "system.info"}},
		}, true)
		checkCode(t, err, "INVALID_ARGUMENT")
		if f.calls != 0 {
			t.Fatal("reserved manifest name reached backend")
		}
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

func TestEvidenceDryRunRedactsOnlyHostDestinationWithoutMutatingRequest(t *testing.T) {
	f := newFixture(t)
	f.bridge.options.DryRun = true
	p := map[string]any{"output_directory": "synthetic-private-directory", "observations": []any{map[string]any{"name": "fixture", "action": "processes.list", "params": map[string]any{"pid": 42}}}}
	result, err := f.bridge.Developer(context.Background(), "11111111-1111-4111-8111-111111111111", "evidence.capture", p, true)
	if err != nil {
		t.Fatal(err)
	}
	preview := mapValue(t, mapValue(t, result["data"])["params"])
	if preview["output_directory"] != "[redacted]" || p["output_directory"] != "synthetic-private-directory" || f.calls != 0 {
		t.Fatal("evidence preview disclosed the destination, mutated the request, or ran transport")
	}
}

func TestEvidencePreparesEveryArtifactBeforeBudgetedWrites(t *testing.T) {
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	result := Success(map[string]any{"png_base64": base64.StdEncoding.EncodeToString(imageBytes.Bytes()), "width": 2, "height": 2})
	artifacts, err := prepareEvidencePayload("fixture", "screenshot.capture", result)
	if err != nil || len(artifacts) != 2 {
		t.Fatal("could not prepare synthetic image")
	}
	directory := t.TempDir()
	_, err = saveEvidenceArtifacts(directory, artifacts, evidenceArtifactBytes(artifacts)-1)
	checkCode(t, err, "EVIDENCE_BUDGET_EXCEEDED")
	files, err := os.ReadDir(directory)
	if err != nil || len(files) != 0 {
		t.Fatal("an aggregate budget rejection left an unregistered image or JSON")
	}
	// Encoding the metadata can fail after image decoding. Preparation must not
	// leave the PNG behind when its pretty JSON exceeds the per-file limit.
	mapValue(t, result["data"])["synthetic_padding"] = string(bytes.Repeat([]byte{'x'}, MaxOutputBytes))
	_, err = evidencePayload(directory, "fixture", "screenshot.capture", result)
	checkCode(t, err, "OUTPUT_LIMIT_EXCEEDED")
	files, err = os.ReadDir(directory)
	if err != nil || len(files) != 0 {
		t.Fatal("a metadata encoding rejection left an unregistered image")
	}
}

func TestEvidenceRollsBackOnlyItsOwnArtifactsOnExclusiveCreateFailure(t *testing.T) {
	directory := t.TempDir()
	existing := filepath.Join(directory, "fixture.json")
	if err := os.WriteFile(existing, []byte("preexisting fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := saveEvidenceArtifacts(directory, []evidenceArtifact{{"fixture.png", []byte("generated fixture")}, {"fixture.json", []byte("replacement")}}, 1024)
	if err == nil {
		t.Fatal("exclusive create replaced a preexisting file")
	}
	content, readErr := os.ReadFile(existing)
	if readErr != nil || string(content) != "preexisting fixture" {
		t.Fatal("rollback changed an artifact that the call did not create")
	}
	if _, err := os.Stat(filepath.Join(directory, "fixture.png")); !os.IsNotExist(err) {
		t.Fatal("rollback left its unregistered PNG")
	}
}

func TestEvidenceTotalBudgetIncludesPrettyArtifactsAndManifest(t *testing.T) {
	padding := make([]any, 50000)
	for i := range padding {
		padding[i] = "generated"
	}
	response := Result{"ok": true, "data": map[string]any{
		"processes": []any{map[string]any{"pid": 42, "name": "SyntheticFixture"}}, "limit": 1, "truncated": false,
		"synthetic_padding": padding, "synthetic_blob": string(bytes.Repeat([]byte{'x'}, 4650000)),
	}, "meta": map[string]any{"protocol_version": "1", "action": "processes.list", "session_id": 1, "user_interactive": true}}
	compact, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareEvidencePayload("fixture-a", "processes.list", response)
	if err != nil {
		t.Fatal(err)
	}
	size := evidenceArtifactBytes(prepared)
	if size*5+int64(len(compact)) > maxEvidenceRunBytes || size*6 <= maxEvidenceRunBytes || len(compact) >= MaxOutputBytes {
		t.Fatal("fixture does not reproduce the compact-JSON budget underestimate")
	}
	items := []any{}
	for i := 0; i < 8; i++ {
		items = append(items, map[string]any{"name": "fixture-" + string(rune('a'+i)), "action": "processes.list", "params": map[string]any{"pid": 42, "limit": 1}})
	}
	p, err := normalizeEvidence(map[string]any{"output_directory": t.TempDir(), "observations": items})
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t)
	f.response.stdout = string(compact)
	result, err := f.bridge.captureEvidence(context.Background(), "11111111-1111-4111-8111-111111111111", p)
	if err != nil || mapValue(t, result["data"])["complete"] != false || f.calls != 6 {
		t.Fatalf("capture did not stop with partial evidence at the budget: calls=%d err=%v", f.calls, err)
	}
	manifestPath := mapValue(t, result["data"])["manifest_file"].(string)
	content, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal("partial capture lost its manifest")
	}
	var manifest map[string]any
	if json.Unmarshal(content, &manifest) != nil || manifest["stop_reason"] != "budget_exhausted" || manifest["complete"] != false {
		t.Fatal("partial manifest lost its budget status")
	}
	registered := map[string]bool{"manifest.json": true}
	for _, observation := range manifest["observations"].([]any) {
		for _, file := range observation.(map[string]any)["files"].([]any) {
			registered[file.(map[string]any)["name"].(string)] = true
		}
	}
	entries, err := os.ReadDir(filepath.Dir(manifestPath))
	if err != nil {
		t.Fatal(err)
	}
	var actualBytes int64
	for _, entry := range entries {
		if !registered[entry.Name()] {
			t.Fatal("capture left a file outside its partial manifest")
		}
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		actualBytes += info.Size()
	}
	if actualBytes > maxEvidenceRunBytes || manifest["bytes"] != float64(actualBytes) || len(content) > evidenceManifestReserve {
		t.Fatalf("run budget or reported total excluded actual files: total=%d reported=%v", actualBytes, manifest["bytes"])
	}
}
