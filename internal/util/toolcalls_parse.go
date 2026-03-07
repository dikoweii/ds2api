package util

import (
	"encoding/json"
	"encoding/xml"
	"regexp"
	"strings"
)

var toolNameLoosePattern = regexp.MustCompile(`[^a-z0-9]+`)
var xmlToolCallPattern = regexp.MustCompile(`(?is)<tool_call>\s*(.*?)\s*</tool_call>`)
var functionCallPattern = regexp.MustCompile(`(?is)<function_call>\s*([^<]+?)\s*</function_call>`)
var functionParamPattern = regexp.MustCompile(`(?is)<function\s+parameter\s+name="([^"]+)"\s*>\s*(.*?)\s*</function\s+parameter>`)
var antmlFunctionCallPattern = regexp.MustCompile(`(?is)<(?:[a-z0-9_]+:)?function_call[^>]*name="([^"]+)"[^>]*>\s*(.*?)\s*</(?:[a-z0-9_]+:)?function_call>`)
var antmlArgumentPattern = regexp.MustCompile(`(?is)<(?:[a-z0-9_]+:)?argument\s+name="([^"]+)"\s*>\s*(.*?)\s*</(?:[a-z0-9_]+:)?argument>`)
var invokeCallPattern = regexp.MustCompile(`(?is)<invoke\s+name="([^"]+)"\s*>(.*?)</invoke>`)
var invokeParamPattern = regexp.MustCompile(`(?is)<parameter\s+name="([^"]+)"\s*>\s*(.*?)\s*</parameter>`)

type ParsedToolCall struct {
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

type ToolCallParseResult struct {
	Calls             []ParsedToolCall
	SawToolCallSyntax bool
	RejectedByPolicy  bool
	RejectedToolNames []string
}

func ParseToolCalls(text string, availableToolNames []string) []ParsedToolCall {
	return ParseToolCallsDetailed(text, availableToolNames).Calls
}

func ParseToolCallsDetailed(text string, availableToolNames []string) ToolCallParseResult {
	result := ToolCallParseResult{}
	if strings.TrimSpace(text) == "" {
		return result
	}
	text = stripFencedCodeBlocks(text)
	if strings.TrimSpace(text) == "" {
		return result
	}
	result.SawToolCallSyntax = strings.Contains(strings.ToLower(text), "tool_calls")

	candidates := buildToolCallCandidates(text)
	var parsed []ParsedToolCall
	for _, candidate := range candidates {
		if tc := parseToolCallsPayload(candidate); len(tc) > 0 {
			parsed = tc
			result.SawToolCallSyntax = true
			break
		}
	}
	if len(parsed) == 0 {
		parsed = parseXMLToolCalls(text)
		if len(parsed) == 0 {
			return result
		}
		result.SawToolCallSyntax = true
	}

	calls, rejectedNames := filterToolCallsDetailed(parsed, availableToolNames)
	result.Calls = calls
	result.RejectedToolNames = rejectedNames
	result.RejectedByPolicy = len(rejectedNames) > 0 && len(calls) == 0
	return result
}

func ParseStandaloneToolCalls(text string, availableToolNames []string) []ParsedToolCall {
	return ParseStandaloneToolCallsDetailed(text, availableToolNames).Calls
}

func ParseStandaloneToolCallsDetailed(text string, availableToolNames []string) ToolCallParseResult {
	result := ToolCallParseResult{}
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return result
	}
	if looksLikeToolExampleContext(trimmed) {
		return result
	}
	result.SawToolCallSyntax = strings.Contains(strings.ToLower(trimmed), "tool_calls")
	candidates := []string{trimmed}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if !strings.HasPrefix(candidate, "{") && !strings.HasPrefix(candidate, "[") {
			continue
		}
		if parsed := parseToolCallsPayload(candidate); len(parsed) > 0 {
			result.SawToolCallSyntax = true
			calls, rejectedNames := filterToolCallsDetailed(parsed, availableToolNames)
			result.Calls = calls
			result.RejectedToolNames = rejectedNames
			result.RejectedByPolicy = len(rejectedNames) > 0 && len(calls) == 0
			return result
		}
	}
	return result
}

func filterToolCallsDetailed(parsed []ParsedToolCall, availableToolNames []string) ([]ParsedToolCall, []string) {
	allowed := map[string]struct{}{}
	allowedCanonical := map[string]string{}
	for _, name := range availableToolNames {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			continue
		}
		allowed[trimmed] = struct{}{}
		lower := strings.ToLower(trimmed)
		if _, exists := allowedCanonical[lower]; !exists {
			allowedCanonical[lower] = trimmed
		}
	}
	if len(allowed) == 0 {
		rejectedSet := map[string]struct{}{}
		for _, tc := range parsed {
			if tc.Name == "" {
				continue
			}
			rejectedSet[tc.Name] = struct{}{}
		}
		rejected := make([]string, 0, len(rejectedSet))
		for name := range rejectedSet {
			rejected = append(rejected, name)
		}
		return nil, rejected
	}
	out := make([]ParsedToolCall, 0, len(parsed))
	rejectedSet := map[string]struct{}{}
	for _, tc := range parsed {
		if tc.Name == "" {
			continue
		}
		matchedName := resolveAllowedToolName(tc.Name, allowed, allowedCanonical)
		if matchedName == "" {
			rejectedSet[tc.Name] = struct{}{}
			continue
		}
		tc.Name = matchedName
		if tc.Input == nil {
			tc.Input = map[string]any{}
		}
		out = append(out, tc)
	}
	rejected := make([]string, 0, len(rejectedSet))
	for name := range rejectedSet {
		rejected = append(rejected, name)
	}
	return out, rejected
}

func resolveAllowedToolName(name string, allowed map[string]struct{}, allowedCanonical map[string]string) string {
	if _, ok := allowed[name]; ok {
		return name
	}
	lower := strings.ToLower(strings.TrimSpace(name))
	if canonical, ok := allowedCanonical[lower]; ok {
		return canonical
	}
	if idx := strings.LastIndex(lower, "."); idx >= 0 && idx < len(lower)-1 {
		if canonical, ok := allowedCanonical[lower[idx+1:]]; ok {
			return canonical
		}
	}
	loose := toolNameLoosePattern.ReplaceAllString(lower, "")
	if loose == "" {
		return ""
	}
	for candidateLower, canonical := range allowedCanonical {
		if toolNameLoosePattern.ReplaceAllString(candidateLower, "") == loose {
			return canonical
		}
	}
	return ""
}

func parseToolCallsPayload(payload string) []ParsedToolCall {
	var decoded any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		return nil
	}
	switch v := decoded.(type) {
	case map[string]any:
		if tc, ok := v["tool_calls"]; ok {
			return parseToolCallList(tc)
		}
		if parsed, ok := parseToolCallItem(v); ok {
			return []ParsedToolCall{parsed}
		}
	case []any:
		return parseToolCallList(v)
	}
	return nil
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

func parseToolCallInput(v any) map[string]any {
	switch x := v.(type) {
	case nil:
		return map[string]any{}
	case map[string]any:
		return x
	case string:
		raw := strings.TrimSpace(x)
		if raw == "" {
			return map[string]any{}
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil && parsed != nil {
			return parsed
		}
		return map[string]any{"_raw": raw}
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return map[string]any{}
		}
		var parsed map[string]any
		if err := json.Unmarshal(b, &parsed); err == nil && parsed != nil {
			return parsed
		}
		return map[string]any{}
	}
}

func parseXMLToolCalls(text string) []ParsedToolCall {
	matches := xmlToolCallPattern.FindAllString(text, -1)
	out := make([]ParsedToolCall, 0, len(matches)+1)
	for _, block := range matches {
		call, ok := parseSingleXMLToolCall(block)
		if !ok {
			continue
		}
		out = append(out, call)
	}
	if len(out) > 0 {
		return out
	}
	if call, ok := parseFunctionCallTagStyle(text); ok {
		return []ParsedToolCall{call}
	}
	if call, ok := parseAntmlFunctionCallStyle(text); ok {
		return []ParsedToolCall{call}
	}
	if call, ok := parseInvokeFunctionCallStyle(text); ok {
		return []ParsedToolCall{call}
	}
	return nil
}

func parseSingleXMLToolCall(block string) (ParsedToolCall, bool) {
	inner := strings.TrimSpace(block)
	inner = strings.TrimPrefix(inner, "<tool_call>")
	inner = strings.TrimSuffix(inner, "</tool_call>")
	inner = strings.TrimSpace(inner)
	if strings.HasPrefix(inner, "{") {
		var payload map[string]any
		if err := json.Unmarshal([]byte(inner), &payload); err == nil {
			name := strings.TrimSpace(asString(payload["tool"]))
			if name == "" {
				name = strings.TrimSpace(asString(payload["tool_name"]))
			}
			if name != "" {
				input := map[string]any{}
				if params, ok := payload["params"].(map[string]any); ok {
					input = params
				} else if params, ok := payload["parameters"].(map[string]any); ok {
					input = params
				}
				return ParsedToolCall{Name: name, Input: input}, true
			}
		}
	}

	dec := xml.NewDecoder(strings.NewReader(block))
	name := ""
	params := map[string]any{}
	inParams := false
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch strings.ToLower(start.Name.Local) {
		case "parameters":
			inParams = true
		case "tool_name", "name":
			var v string
			if err := dec.DecodeElement(&v, &start); err == nil && strings.TrimSpace(v) != "" {
				name = strings.TrimSpace(v)
			}
		default:
			if inParams {
				var v string
				if err := dec.DecodeElement(&v, &start); err == nil {
					params[start.Name.Local] = strings.TrimSpace(v)
				}
			}
		}
	}
	if strings.TrimSpace(name) == "" {
		return ParsedToolCall{}, false
	}
	return ParsedToolCall{Name: strings.TrimSpace(name), Input: params}, true
}

func parseFunctionCallTagStyle(text string) (ParsedToolCall, bool) {
	m := functionCallPattern.FindStringSubmatch(text)
	if len(m) < 2 {
		return ParsedToolCall{}, false
	}
	name := strings.TrimSpace(m[1])
	if name == "" {
		return ParsedToolCall{}, false
	}
	input := map[string]any{}
	for _, pm := range functionParamPattern.FindAllStringSubmatch(text, -1) {
		if len(pm) < 3 {
			continue
		}
		key := strings.TrimSpace(pm[1])
		val := strings.TrimSpace(pm[2])
		if key != "" {
			input[key] = val
		}
	}
	return ParsedToolCall{Name: name, Input: input}, true
}

func parseAntmlFunctionCallStyle(text string) (ParsedToolCall, bool) {
	m := antmlFunctionCallPattern.FindStringSubmatch(text)
	if len(m) < 3 {
		return ParsedToolCall{}, false
	}
	name := strings.TrimSpace(m[1])
	if name == "" {
		return ParsedToolCall{}, false
	}
	body := strings.TrimSpace(m[2])
	input := map[string]any{}
	if strings.HasPrefix(body, "{") {
		if err := json.Unmarshal([]byte(body), &input); err == nil {
			return ParsedToolCall{Name: name, Input: input}, true
		}
	}
	for _, am := range antmlArgumentPattern.FindAllStringSubmatch(body, -1) {
		if len(am) < 3 {
			continue
		}
		k := strings.TrimSpace(am[1])
		v := strings.TrimSpace(am[2])
		if k != "" {
			input[k] = v
		}
	}
	return ParsedToolCall{Name: name, Input: input}, true
}

func parseInvokeFunctionCallStyle(text string) (ParsedToolCall, bool) {
	m := invokeCallPattern.FindStringSubmatch(text)
	if len(m) < 3 {
		return ParsedToolCall{}, false
	}
	name := strings.TrimSpace(m[1])
	if name == "" {
		return ParsedToolCall{}, false
	}
	input := map[string]any{}
	for _, pm := range invokeParamPattern.FindAllStringSubmatch(m[2], -1) {
		if len(pm) < 3 {
			continue
		}
		k := strings.TrimSpace(pm[1])
		v := strings.TrimSpace(pm[2])
		if k != "" {
			input[k] = v
		}
	}
	return ParsedToolCall{Name: name, Input: input}, true
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
