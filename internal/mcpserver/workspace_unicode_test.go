package mcpserver

import (
	"path/filepath"
	"testing"
)

func TestInputTextMCPWorkspaceRejectsInvalidRawUnicodeBeforeBackend(t *testing.T) {
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
			raw := `{"version":1,"vm":"` + accessUUIDA + `","steps":[{"name":"type","action":"input.text","params":{"hwnd":"0x123","pid":42,"start_time_utc":"2026-01-02T03:04:05Z","text":` + test.literal + `}}]}`
			path := writeSyntheticFile(t, root, "profile.json", raw)
			profile, err := readWorkspaceJSON(path)
			if test.valid {
				if err != nil {
					t.Fatal(err)
				}
				step := profile["steps"].([]any)[0].(map[string]any)
				if step["params"].(map[string]any)["text"] != test.want {
					t.Fatal("workspace decoder changed valid text")
				}
				return // File validation never invokes a developer backend.
			}
			if err == nil {
				t.Fatal("workspace profile accepted invalid raw Unicode")
			}
			f := &developerFixture{}
			session, ctx := connectDeveloper(t, f, ServerOptions{DeveloperTools: true, AllowGuestWrite: true, DeveloperWorkspace: root})
			payload := envelope(t, call(t, session, ctx, "dev.workflow.run", devArgs(accessUUIDA, workflowRequest(path, filepath.Join(root, "runs")))))
			requireCode(t, payload, "WORKSPACE_ACCESS_DENIED")
			if f.calls != 0 || f.inventories != 0 {
				t.Fatal("invalid Unicode profile reached the backend")
			}
		})
	}
}
