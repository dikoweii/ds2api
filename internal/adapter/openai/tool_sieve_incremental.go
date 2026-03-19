package openai

import "strings"

func buildIncrementalToolDeltas(state *toolStreamSieveState) []toolCallDelta {
	if state.disableDeltas {
		return nil
	}
	captured := state.capture.String()
	if captured == "" {
		return nil
	}
	lower := strings.ToLower(captured)
	keyIdx := strings.Index(lower, "tool_calls")
	if keyIdx < 0 {
		return nil
	}
	start := strings.LastIndex(captured[:keyIdx], "{")
	if start < 0 {
		return nil
	}
	certainSingle, hasMultiple := classifyToolCallsIncrementalSafety(captured, keyIdx)
	if hasMultiple {
		state.disableDeltas = true
		return nil
	}
	if !certainSingle {
		// In uncertain phases (e.g. first call arrived but array not closed yet),
		// avoid speculative deltas and wait for final parsed tool_calls payload.
		return nil
	}
	callStart, ok := findFirstToolCallObjectStart(captured, keyIdx)
	if !ok {
		return nil
	}
	deltas := make([]toolCallDelta, 0, 2)
	if state.toolName == "" {
		name, ok := extractToolCallName(captured, callStart)
		if !ok || name == "" {
			return nil
		}
		state.toolName = name
	}
	if state.toolArgsStart < 0 {
		argsStart, stringMode, ok := findToolCallArgsStart(captured, callStart)
		if ok {
			state.toolArgsString = stringMode
			if stringMode {
				state.toolArgsStart = argsStart + 1
			} else {
				state.toolArgsStart = argsStart
			}
			state.toolArgsSent = state.toolArgsStart
		}
	}
	if !state.toolNameSent {
		if state.toolArgsStart < 0 {
			return nil
		}
		state.toolNameSent = true
		deltas = append(deltas, toolCallDelta{Index: 0, Name: state.toolName})
	}
	if state.toolArgsStart < 0 || state.toolArgsDone {
		return deltas
	}
	end, complete, ok := scanToolCallArgsProgress(captured, state.toolArgsStart, state.toolArgsString)
	if !ok {
		return deltas
	}
	if end > state.toolArgsSent {
		deltas = append(deltas, toolCallDelta{
			Index:     0,
			Arguments: captured[state.toolArgsSent:end],
		})
		state.toolArgsSent = end
	}
	if complete {
		state.toolArgsDone = true
	}
	return deltas
}

func classifyToolCallsIncrementalSafety(text string, keyIdx int) (certainSingle bool, hasMultiple bool) {
	arrStart, ok := findToolCallsArrayStart(text, keyIdx)
	if !ok {
		return false, false
	}
	i := skipSpaces(text, arrStart+1)
	if i >= len(text) || text[i] != '{' {
		return false, false
	}
	count := 0
	depth := 0
	quote := byte(0)
	escaped := false
	for ; i < len(text); i++ {
		ch := text[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '"' || ch == '\'' {
			quote = ch
			continue
		}
		if ch == '{' {
			if depth == 0 {
				count++
				if count > 1 {
					return false, true
				}
			}
			depth++
			continue
		}
		if ch == '}' {
			if depth > 0 {
				depth--
			}
			continue
		}
		if ch == ',' && depth == 0 {
			// top-level separator means at least one more tool call exists
			// (or is expected). Treat as multi-call and stop incremental deltas.
			return false, true
		}
		if ch == ']' && depth == 0 {
			return count == 1, false
		}
	}
	// array not closed yet: still uncertain whether more calls will appear
	return false, false
}

func findFirstToolCallObjectStart(text string, keyIdx int) (int, bool) {
	arrStart, ok := findToolCallsArrayStart(text, keyIdx)
	if !ok {
		return -1, false
	}
	i := skipSpaces(text, arrStart+1)
	if i >= len(text) || text[i] != '{' {
		return -1, false
	}
	return i, true
}

func findToolCallsArrayStart(text string, keyIdx int) (int, bool) {
	i := keyIdx + len("tool_calls")
	for i < len(text) && text[i] != ':' {
		i++
	}
	if i >= len(text) {
		return -1, false
	}
	i = skipSpaces(text, i+1)
	if i >= len(text) || text[i] != '[' {
		return -1, false
	}
	return i, true
}

func extractToolCallName(text string, callStart int) (string, bool) {
	valueStart, ok := findObjectFieldValueStart(text, callStart, []string{"name"})
	if !ok || valueStart >= len(text) || text[valueStart] != '"' {
		fnStart, fnOK := findFunctionObjectStart(text, callStart)
		if !fnOK {
			return "", false
		}
		valueStart, ok = findObjectFieldValueStart(text, fnStart, []string{"name"})
		if !ok || valueStart >= len(text) || text[valueStart] != '"' {
			return "", false
		}
	}
	name, _, ok := parseJSONStringLiteral(text, valueStart)
	if !ok {
		return "", false
	}
	return name, true
}

func findToolCallArgsStart(text string, callStart int) (int, bool, bool) {
	keys := []string{"input", "arguments", "args", "parameters", "params"}
	valueStart, ok := findObjectFieldValueStart(text, callStart, keys)
	if !ok {
		fnStart, fnOK := findFunctionObjectStart(text, callStart)
		if !fnOK {
			return -1, false, false
		}
		valueStart, ok = findObjectFieldValueStart(text, fnStart, keys)
		if !ok {
			return -1, false, false
		}
	}
	if valueStart >= len(text) {
		return -1, false, false
	}
	ch := text[valueStart]
	if ch == '{' || ch == '[' {
		return valueStart, false, true
	}
	if ch == '"' {
		return valueStart, true, true
	}
	return -1, false, false
}

func scanToolCallArgsProgress(text string, start int, stringMode bool) (int, bool, bool) {
	if start < 0 || start > len(text) {
		return 0, false, false
	}
	if stringMode {
		escaped := false
		for i := start; i < len(text); i++ {
			ch := text[i]
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == '"' {
				return i, true, true
			}
		}
		return len(text), false, true
	}
	if start >= len(text) {
		return start, false, false
	}
	if text[start] != '{' && text[start] != '[' {
		return 0, false, false
	}
	depth := 0
	quote := byte(0)
	escaped := false
	for i := start; i < len(text); i++ {
		ch := text[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '"' || ch == '\'' {
			quote = ch
			continue
		}
		if ch == '{' || ch == '[' {
			depth++
			continue
		}
		if ch == '}' || ch == ']' {
			depth--
			if depth == 0 {
				return i + 1, true, true
			}
		}
	}
	return len(text), false, true
}

func findFunctionObjectStart(text string, callStart int) (int, bool) {
	valueStart, ok := findObjectFieldValueStart(text, callStart, []string{"function"})
	if !ok || valueStart >= len(text) || text[valueStart] != '{' {
		return -1, false
	}
	return valueStart, true
}
