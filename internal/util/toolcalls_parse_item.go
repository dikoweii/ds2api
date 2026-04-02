package util

import "strings"

func isLikelyJSONToolPayloadCandidate(candidate string) bool {
	trimmed := strings.TrimSpace(candidate)
	if trimmed == "" {
		return false
	}
	if !(strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) {
		return false
	}
	lower := strings.ToLower(trimmed)
	return strings.Contains(lower, "tool_calls") ||
		strings.Contains(lower, "\"function\"") ||
		strings.Contains(lower, "functioncall") ||
		strings.Contains(lower, "\"tool_use\"")
}

func parseToolCallList(v any) []ParsedToolCall {
	items, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]ParsedToolCall, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if tc, ok := parseToolCallItem(m); ok {
			out = append(out, tc)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseToolCallItem(m map[string]any) (ParsedToolCall, bool) {
	name, _ := m["name"].(string)
	inputRaw, hasInput := m["input"]
	if fnCall, ok := m["functionCall"].(map[string]any); ok {
		if name == "" {
			name, _ = fnCall["name"].(string)
		}
		if !hasInput {
			if v, ok := fnCall["args"]; ok {
				inputRaw = v
				hasInput = true
			}
		}
		if !hasInput {
			if v, ok := fnCall["arguments"]; ok {
				inputRaw = v
				hasInput = true
			}
		}
	}
	if fn, ok := m["function"].(map[string]any); ok {
		if name == "" {
			name, _ = fn["name"].(string)
		}
		if !hasInput {
			if v, ok := fn["arguments"]; ok {
				inputRaw = v
				hasInput = true
			}
		}
	}
	if !hasInput {
		for _, key := range []string{"arguments", "args", "parameters", "params"} {
			if v, ok := m[key]; ok {
				inputRaw = v
				hasInput = true
				break
			}
		}
	}
	if strings.TrimSpace(name) == "" {
		return ParsedToolCall{}, false
	}
	return ParsedToolCall{
		Name:  strings.TrimSpace(name),
		Input: parseToolCallInput(inputRaw),
	}, true
}
