package mcpserver

// All tools use this extensible envelope. Data varies by tool; the guest adapter
// validates its action-specific fields before the response reaches MCP.
func outputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ok":   map[string]any{"type": "boolean"},
			"data": map[string]any{},
			"meta": map[string]any{"type": "object"},
			"error": map[string]any{"type": "object", "required": []string{"code", "message"}, "properties": map[string]any{
				"code": map[string]any{"type": "string", "minLength": 1}, "message": map[string]any{"type": "string", "minLength": 1},
			}},
		},
		"required": []string{"ok"},
		"oneOf": []any{
			map[string]any{"properties": map[string]any{"ok": map[string]any{"const": true}}, "required": []string{"data"}, "not": map[string]any{"required": []string{"error"}}},
			map[string]any{"properties": map[string]any{"ok": map[string]any{"const": false}}, "required": []string{"error"}, "not": map[string]any{"required": []string{"data"}}},
		},
	}
}
