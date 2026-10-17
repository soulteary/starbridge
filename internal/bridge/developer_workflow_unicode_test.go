package bridge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInputTextWorkflowProfileRejectsInvalidRawUnicodeBeforeBackend(t *testing.T) {
	for _, test := range []struct {
		name, literal, want string
		valid               bool
	}{
		{"high surrogate", `"\ud800"`, "", false},
		{"low surrogate", `"\udfff"`, "", false},
		{"wrong pair", `"\ud800\u0041"`, "", false},
		{"invalid UTF8", "\"\xff\"", "", false},
		{"valid pair", `"\ud83d\ude00"`, "😀", true},
		{"literal replacement", `"�"`, "�", true},
		{"escaped replacement", `"\ufffd"`, "�", true},
		{"escaped backslash", `"\\ud800"`, `\ud800`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "profile.json")
			raw := `{"version":1,"vm":"` + syntheticWorkflowVM + `","steps":[{"name":"type","action":"input.text","params":{"hwnd":"0x123","pid":42,"start_time_utc":"2026-01-02T03:04:05Z","text":` + test.literal + `}}]}`
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			profile, _, err := LoadDevelopmentProfile(path)
			if test.valid {
				if err != nil || profile.Steps[0].Params["text"] != test.want {
					t.Fatalf("valid text changed: %v", err)
				}
				if _, err := normalizeDeveloperInputText(profile.Steps[0].Params); err != nil {
					t.Fatal(err)
				}
				return // Loading a valid profile never dispatches a step.
			}
			checkCode(t, err, "INVALID_PROFILE")
			for _, action := range []string{"workflow.run", "workflow.cleanup"} {
				f := newFixture(t)
				output := filepath.Join(root, "runs")
				_, err := f.bridge.Developer(context.Background(), syntheticWorkflowVM, action, map[string]any{"profile_file": path, "output_directory": output}, true)
				failure := checkCode(t, err, "INVALID_PROFILE")
				if f.calls != 0 || strings.Contains(failure.Message, test.literal) {
					t.Fatal("invalid raw Unicode reached backend or leaked into an error")
				}
				if _, err := os.Stat(output); !os.IsNotExist(err) {
					t.Fatal("invalid profile created workflow output")
				}
			}
		})
	}
}
