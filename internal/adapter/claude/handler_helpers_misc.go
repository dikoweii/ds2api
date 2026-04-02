package claude

import (
	"fmt"
	"strings"
)

func hasSystemMessage(messages []any) bool {
	for _, m := range messages {
		msg, ok := m.(map[string]any)
		if ok && msg["role"] == "system" {
			return true
		}
	}
	return false
}

func extractClaudeToolNames(tools []any) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		m, ok := t.(map[string]any)
		if !ok {
			continue
		}
		name, _, _ := extractClaudeToolMeta(m)
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

func extractClaudeToolMeta(m map[string]any) (string, string, any) {
	name, _ := m["name"].(string)
	desc, _ := m["description"].(string)
	schemaObj := m["input_schema"]
	if schemaObj == nil {
		schemaObj = m["parameters"]
	}

	if fn, ok := m["function"].(map[string]any); ok {
		if strings.TrimSpace(name) == "" {
			name, _ = fn["name"].(string)
		}
		if strings.TrimSpace(desc) == "" {
			desc, _ = fn["description"].(string)
		}
		if schemaObj == nil {
			if v, ok := fn["input_schema"]; ok {
				schemaObj = v
			}
		}
		if schemaObj == nil {
			if v, ok := fn["parameters"]; ok {
				schemaObj = v
			}
		}
	}
	return strings.TrimSpace(name), strings.TrimSpace(desc), schemaObj
}

func toMessageMaps(v any) []map[string]any {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(arr))
	for _, item := range arr {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func extractMessageContent(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		parts := make([]string, 0, len(x))
		for _, it := range x {
			parts = append(parts, fmt.Sprintf("%v", it))
		}
		return strings.Join(parts, "\n")
	default:
		return fmt.Sprintf("%v", x)
	}
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
