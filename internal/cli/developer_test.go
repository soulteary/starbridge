package cli

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/soulteary/starbridge/internal/bridge"
)

func TestDeveloperCLIParametersAndDryRun(t *testing.T) {
	path := filepath.Join(t.TempDir(), "parameters with spaces.json")
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, request := range [][]string{nil, {"--request", path}} {
		args := []string{"--dry-run", "--prlctl", "/uninstalled/prlctl", "--developer-script", `D:\Helpers\developer's entry.ps1`, "dev", "system.info", "Windows 中文;$(never)"}
		status, result := invoke(t, append(args, request...)...)
		if status != 0 {
			t.Fatal(result)
		}
		data := resultData(t, result)
		argv := argumentVector(t, data)
		if argv[2] != "Windows 中文;$(never)" || argv[11] != `D:\Helpers\developer's entry.ps1` || data["mutation"] != false {
			t.Fatal(argv)
		}
		decoded, err := base64.StdEncoding.DecodeString(argv[len(argv)-1])
		if err != nil || string(decoded) != `{"action":"system.info","params":{}}` {
			t.Fatalf("request=%s err=%v", decoded, err)
		}
	}
}

func TestDeveloperCLIRejectsInvalidRequestFiles(t *testing.T) {
	for _, test := range []struct{ name, text string }{
		{"null", "null"}, {"array", "[]"}, {"singleton array", "[{}]"}, {"boolean", "true"},
		{"malformed", "{"}, {"trailing", "{} {}"}, {"field", `{"unexpected":true}`},
		{"oversize", strings.Repeat(" ", bridge.MaxDeveloperRequestBytes+1)}, {"invalid UTF8", string([]byte{0xff})},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "request.json")
			if err := os.WriteFile(path, []byte(test.text), 0600); err != nil {
				t.Fatal(err)
			}
			status, result := invoke(t, "--dry-run", "dev", "system.info", "vm", "--request", path)
			if status != 1 {
				t.Fatal(status)
			}
			assertCode(t, result, "INVALID_ARGUMENT")
		})
	}
	for _, args := range [][]string{
		{"--dry-run", "dev", "system.info", "vm", "--request="},
		{"--dry-run", "dev", "system.info", "vm", "--request", t.TempDir()},
		{"--dry-run", "--developer-script=", "dev", "system.info", "vm"},
		{"--dry-run", "--developer-script", "relative.ps1", "dev", "system.info", "vm"},
	} {
		status, result := invoke(t, args...)
		if status != 1 {
			t.Fatal(status)
		}
		assertCode(t, result, "INVALID_ARGUMENT")
	}
	status, result := invoke(t, "--dry-run", "dev", "missing.action", "vm", "--allow-write")
	if status != 1 {
		t.Fatal(status)
	}
	assertCode(t, result, "UNKNOWN_ACTION")
}

func TestDeveloperRequestRetainsExactJSONNumbers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(path, []byte(`{"integer":9007199254740993,"array":[1],"empty":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	params, err := developerRequest(map[string]string{"request": path})
	if err != nil || params["integer"] != json.Number("9007199254740993") || !reflect.DeepEqual(params["empty"], []any{}) {
		t.Fatalf("params=%#v err=%v", params, err)
	}
}
