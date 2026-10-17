package cli

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInputTextCLIUnicodeAndPrivatePreview(t *testing.T) {
	path := filepath.Join(t.TempDir(), "parameters.json")
	for _, test := range []struct {
		literal, want string
		valid         bool
	}{
		{`"中文\ud83d\ude00\r\n\t"`, "中文😀\r\n\t", true},
		{`"\\ud800"`, `\ud800`, true},
		{`"�"`, "�", true}, {`"\ufffd"`, "�", true},
		{`"\ud800"`, "", false}, {`"\udfff"`, "", false},
		{"\"\xff\"", "", false},
	} {
		raw := `{"hwnd":"0x123","pid":42,"start_time_utc":"2026-01-02T03:04:05Z","text":` + test.literal + `}`
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		params, err := developerRequest(map[string]string{"request": path})
		if !test.valid {
			if err == nil {
				t.Fatal("malformed Unicode accepted")
			}
			continue
		}
		if err != nil || params["text"] != test.want {
			t.Fatalf("Unicode changed: %#v %v", params, err)
		}
		status, result := invoke(t, "--dry-run", "--prlctl", filepath.Join(t.TempDir(), "not-installed"), "dev", "input.text", "fixture-vm", "--request", path, "--allow-write")
		if status != 0 {
			t.Fatalf("preview failed: %#v", result)
		}
		argv := argumentVector(t, resultData(t, result))
		wire, err := base64.StdEncoding.DecodeString(argv[len(argv)-2])
		if err != nil {
			t.Fatal(err)
		}
		var request map[string]any
		if err := json.Unmarshal(wire, &request); err != nil {
			t.Fatal(err)
		}
		if request["params"].(map[string]any)["text"] != "[REDACTED]" || strings.Contains(string(wire), "中文") {
			t.Fatal("CLI preview leaked text")
		}
	}
}
