package claude

import (
	"encoding/json"
	"fmt"
	"strings"
)

func normalizeClaudeMessages(messages []any) []any {
	out := make([]any, 0, len(messages))
	for _, m := range messages {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		copied := cloneMap(msg)
		switch content := msg["content"].(type) {
		case []any:
			parts := make([]string, 0, len(content))
			for _, block := range content {
				b, ok := block.(map[string]any)
				if !ok {
					continue
				}
				typeStr, _ := b["type"].(string)
				if typeStr == "text" {
					if t, ok := b["text"].(string); ok {
						parts = append(parts, t)
					}
				}
				if typeStr == "tool_result" {
					parts = append(parts, formatClaudeToolResultForPrompt(b))
				}
			}
			copied["content"] = strings.Join(parts, "\n")
		}
		out = append(out, copied)
	}
	return out
}

func buildClaudeToolPrompt(tools []any) string {
	parts := []string{"You are Claude, a helpful AI assistant. You have access to these tools:"}
	for _, t := range tools {
		m, ok := t.(map[string]any)
		if !ok {
			continue
		}
		name, desc, schemaObj := extractClaudeToolMeta(m)
		schema, _ := json.Marshal(schemaObj)
		parts = append(parts, fmt.Sprintf("Tool: %s\nDescription: %s\nParameters: %s", name, desc, schema))
	}
	parts = append(parts,
		"When you need a tool, respond with Claude-native tool use (tool_use) using the provided tool schema. Do not print tool-call JSON in text.",
		"History markers in conversation: [TOOL_CALL_HISTORY]...[/TOOL_CALL_HISTORY] are your previous tool calls; [TOOL_RESULT_HISTORY]...[/TOOL_RESULT_HISTORY] are runtime tool outputs, not user input.",
		"After a valid [TOOL_RESULT_HISTORY], continue with final answer instead of repeating the same call unless required fields are still missing.",
	)
	return strings.Join(parts, "\n\n")
}

func formatClaudeToolResultForPrompt(block map[string]any) string {
	if block == nil {
		return ""
	}
	toolCallID := strings.TrimSpace(fmt.Sprintf("%v", block["tool_use_id"]))
	if toolCallID == "" {
		toolCallID = strings.TrimSpace(fmt.Sprintf("%v", block["tool_call_id"]))
	}
	if toolCallID == "" {
		toolCallID = "unknown"
	}
	name := strings.TrimSpace(fmt.Sprintf("%v", block["name"]))
	if name == "" {
		name = "unknown"
	}
	content := strings.TrimSpace(fmt.Sprintf("%v", block["content"]))
	if content == "" {
		content = "null"
	}
	return fmt.Sprintf("[TOOL_RESULT_HISTORY]\nstatus: already_returned\norigin: tool_runtime\nnot_user_input: true\ntool_call_id: %s\nname: %s\ncontent: %s\n[/TOOL_RESULT_HISTORY]", toolCallID, name, content)
}

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
