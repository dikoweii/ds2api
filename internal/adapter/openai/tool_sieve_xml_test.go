package openai

import (
	"strings"
	"testing"
)

func TestProcessToolSieveInterceptsXMLToolCallWithoutLeak(t *testing.T) {
	var state toolStreamSieveState
	// Simulate a model producing XML tool call output chunk by chunk.
	chunks := []string{
		"<tool_calls>\n",
		"  <tool_call>\n",
		"    <tool_name>read_file</tool_name>\n",
		`    <parameters>{"path":"README.MD"}</parameters>` + "\n",
		"  </tool_call>\n",
		"</tool_calls>",
	}
	var events []toolStreamEvent
	for _, c := range chunks {
		events = append(events, processToolSieveChunk(&state, c, []string{"read_file"})...)
	}
	events = append(events, flushToolSieve(&state, []string{"read_file"})...)

	var textContent string
	var toolCalls int
	for _, evt := range events {
		if evt.Content != "" {
			textContent += evt.Content
		}
		toolCalls += len(evt.ToolCalls)
	}

	if strings.Contains(textContent, "<tool_call") {
		t.Fatalf("XML tool call content leaked to text: %q", textContent)
	}
	if strings.Contains(textContent, "read_file") {
		t.Fatalf("tool name leaked to text: %q", textContent)
	}
	if toolCalls == 0 {
		t.Fatal("expected tool calls to be extracted, got none")
	}
}

func TestProcessToolSieveXMLWithLeadingText(t *testing.T) {
	var state toolStreamSieveState
	// Model outputs some prose then an XML tool call.
	chunks := []string{
		"Let me check the file.\n",
		"<tool_calls>\n  <tool_call>\n    <tool_name>read_file</tool_name>\n",
		`    <parameters>{"path":"go.mod"}</parameters>` + "\n  </tool_call>\n</tool_calls>",
	}
	var events []toolStreamEvent
	for _, c := range chunks {
		events = append(events, processToolSieveChunk(&state, c, []string{"read_file"})...)
	}
	events = append(events, flushToolSieve(&state, []string{"read_file"})...)

	var textContent string
	var toolCalls int
	for _, evt := range events {
		if evt.Content != "" {
			textContent += evt.Content
		}
		toolCalls += len(evt.ToolCalls)
	}

	// Leading text should be emitted.
	if !strings.Contains(textContent, "Let me check the file.") {
		t.Fatalf("expected leading text to be emitted, got %q", textContent)
	}
	// The XML itself should NOT leak.
	if strings.Contains(textContent, "<tool_call") {
		t.Fatalf("XML tool call content leaked to text: %q", textContent)
	}
	if toolCalls == 0 {
		t.Fatal("expected tool calls to be extracted, got none")
	}
}

func TestProcessToolSievePartialXMLTagHeldBack(t *testing.T) {
	var state toolStreamSieveState
	// Chunk ends with a partial XML tool tag.
	events := processToolSieveChunk(&state, "Hello <tool_ca", []string{"read_file"})

	var textContent string
	for _, evt := range events {
		textContent += evt.Content
	}

	// "Hello " should be emitted, but "<tool_ca" should be held back.
	if strings.Contains(textContent, "<tool_ca") {
		t.Fatalf("partial XML tag should not be emitted, got %q", textContent)
	}
	if !strings.Contains(textContent, "Hello") {
		t.Fatalf("expected 'Hello' text to be emitted, got %q", textContent)
	}
}

func TestFindToolSegmentStartDetectsXMLToolCalls(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  int
	}{
		{"tool_calls_tag", "some text <tool_calls>\n", 10},
		{"gemini_function_call_json", `some text {"functionCall":{"name":"search","args":{"q":"latest"}}}`, 10},
		{"tool_call_tag", "prefix <tool_call>\n", 7},
		{"invoke_tag", "text <invoke name=\"foo\">body</invoke>", 5},
		{"function_call_tag", "<function_call name=\"foo\">body</function_call>", 0},
		{"no_xml", "just plain text", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := findToolSegmentStart(tc.input)
			if got != tc.want {
				t.Fatalf("findToolSegmentStart(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestFindToolSegmentStartIgnoresFunctionCallProse(t *testing.T) {
	input := "Please explain the functionCall API field and how clients should parse it."
	if got := findToolSegmentStart(input); got != -1 {
		t.Fatalf("expected no tool segment start for prose, got %d", got)
	}
}

func TestFindToolSegmentStartDetectsQuotedFunctionCallKey(t *testing.T) {
	input := `prefix {"functionCall": {"name":"search_web","args":{"query":"x"}}}`
	want := strings.Index(input, "{")
	if got := findToolSegmentStart(input); got != want {
		t.Fatalf("expected JSON object start %d, got %d", want, got)
	}
}

func TestFindToolSegmentStartDetectsLooseFunctionCallKey(t *testing.T) {
	input := `prefix {functionCall: {"name":"search_web","args":{"query":"x"}}}`
	want := strings.Index(input, "{")
	if got := findToolSegmentStart(input); got != want {
		t.Fatalf("expected JSON object start %d, got %d", want, got)
	}
}

func TestFindToolSegmentStartPrefersQuotedFunctionCallOverEarlierBareProse(t *testing.T) {
	input := `prefix {note} functionCall: docs hint {"functionCall":{"name":"search_web","args":{"query":"x"}}}`
	want := strings.Index(input, `{"functionCall"`)
	if got := findToolSegmentStart(input); got != want {
		t.Fatalf("expected quoted functionCall JSON start %d, got %d", want, got)
	}
}

func TestFindToolSegmentStartIgnoresLooseFunctionCallProse(t *testing.T) {
	input := "Please explain why functionCall: is used in documentation examples."
	if got := findToolSegmentStart(input); got != -1 {
		t.Fatalf("expected no tool segment start for prose, got %d", got)
	}
}

func TestProcessToolSieveDoesNotBufferFunctionCallProse(t *testing.T) {
	var state toolStreamSieveState
	chunk := "Please explain the functionCall API field and keep streaming this sentence."
	events := processToolSieveChunk(&state, chunk, []string{"search_web"})
	var text string
	for _, evt := range events {
		text += evt.Content
		if len(evt.ToolCalls) > 0 {
			t.Fatalf("expected no tool calls for prose, got %#v", evt.ToolCalls)
		}
	}
	if text != chunk {
		t.Fatalf("expected prose to pass through immediately, got %q", text)
	}
}

func TestProcessToolSieveDetectsGeminiFunctionCallPayload(t *testing.T) {
	var state toolStreamSieveState
	events := processToolSieveChunk(&state, `{"functionCall":{"name":"search_web","args":{"query":"latest"}}}`, []string{"search_web"})
	events = append(events, flushToolSieve(&state, []string{"search_web"})...)

	var textContent string
	var toolCalls int
	for _, evt := range events {
		if evt.Content != "" {
			textContent += evt.Content
		}
		toolCalls += len(evt.ToolCalls)
	}
	if toolCalls != 1 {
		t.Fatalf("expected one tool call from functionCall payload, got events=%#v", events)
	}
	if strings.Contains(strings.ToLower(textContent), "functioncall") {
		t.Fatalf("functionCall json leaked into text content: %q", textContent)
	}
}

func TestFindPartialXMLToolTagStart(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  int
	}{
		{"partial_tool_call", "Hello <tool_ca", 6},
		{"partial_invoke", "Prefix <inv", 7},
		{"partial_lt_only", "Text <", 5},
		{"complete_tag", "Text <tool_call>done", -1},
		{"no_lt", "plain text", -1},
		{"closed_lt", "a < b > c", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := findPartialXMLToolTagStart(tc.input)
			if got != tc.want {
				t.Fatalf("findPartialXMLToolTagStart(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

func TestHasOpenXMLToolTag(t *testing.T) {
	if !hasOpenXMLToolTag("<tool_call>\n<tool_name>foo</tool_name>") {
		t.Fatal("should detect open XML tool tag without closing tag")
	}
	if hasOpenXMLToolTag("<tool_call>\n<tool_name>foo</tool_name></tool_call>") {
		t.Fatal("should return false when closing tag is present")
	}
	if hasOpenXMLToolTag("plain text without any XML") {
		t.Fatal("should return false for plain text")
	}
}

// Test the EXACT scenario the user reports: token-by-token streaming where
// <tool_calls> tag arrives in small pieces.
func TestProcessToolSieveTokenByTokenXMLNoLeak(t *testing.T) {
	var state toolStreamSieveState
	// Simulate DeepSeek model generating tokens one at a time.
	chunks := []string{
		"<",
		"tool",
		"_calls",
		">\n",
		"  <",
		"tool",
		"_call",
		">\n",
		"    <",
		"tool",
		"_name",
		">",
		"read",
		"_file",
		"</",
		"tool",
		"_name",
		">\n",
		"    <",
		"parameters",
		">",
		`{"path"`,
		`: "README.MD"`,
		`}`,
		"</",
		"parameters",
		">\n",
		"  </",
		"tool",
		"_call",
		">\n",
		"</",
		"tool",
		"_calls",
		">",
	}
	var events []toolStreamEvent
	for _, c := range chunks {
		events = append(events, processToolSieveChunk(&state, c, []string{"read_file"})...)
	}
	events = append(events, flushToolSieve(&state, []string{"read_file"})...)

	var textContent string
	var toolCalls int
	for _, evt := range events {
		if evt.Content != "" {
			textContent += evt.Content
		}
		toolCalls += len(evt.ToolCalls)
	}

	if strings.Contains(textContent, "<tool_call") {
		t.Fatalf("XML tool call content leaked to text in token-by-token mode: %q", textContent)
	}
	if strings.Contains(textContent, "tool_calls>") {
		t.Fatalf("closing tag fragment leaked to text: %q", textContent)
	}
	if strings.Contains(textContent, "read_file") {
		t.Fatalf("tool name leaked to text: %q", textContent)
	}
	if toolCalls == 0 {
		t.Fatal("expected tool calls to be extracted, got none")
	}
}

// Test that flushToolSieve on incomplete XML does NOT leak the raw XML content.
func TestFlushToolSieveIncompleteXMLDoesNotLeak(t *testing.T) {
	var state toolStreamSieveState
	// XML block starts but stream ends before completion.
	chunks := []string{
		"<tool_calls>\n",
		"  <tool_call>\n",
		"    <tool_name>read_file</tool_name>\n",
	}
	var events []toolStreamEvent
	for _, c := range chunks {
		events = append(events, processToolSieveChunk(&state, c, []string{"read_file"})...)
	}
	// Stream ends abruptly - flush should NOT dump raw XML.
	events = append(events, flushToolSieve(&state, []string{"read_file"})...)

	var textContent string
	for _, evt := range events {
		if evt.Content != "" {
			textContent += evt.Content
		}
	}

	if strings.Contains(textContent, "<tool_call") {
		t.Fatalf("incomplete XML leaked on flush: %q", textContent)
	}
}

// Test that the opening tag "<tool_calls>\n  " is NOT emitted as text content.
func TestOpeningXMLTagNotLeakedAsContent(t *testing.T) {
	var state toolStreamSieveState
	// First chunk is the opening tag - should be held, not emitted.
	evts1 := processToolSieveChunk(&state, "<tool_calls>\n  ", []string{"read_file"})
	for _, evt := range evts1 {
		if strings.Contains(evt.Content, "<tool_calls>") {
			t.Fatalf("opening tag leaked on first chunk: %q", evt.Content)
		}
	}

	// Remaining content arrives.
	evts2 := processToolSieveChunk(&state, "<tool_call>\n    <tool_name>read_file</tool_name>\n    <parameters>{\"path\":\"README.MD\"}</parameters>\n  </tool_call>\n</tool_calls>", []string{"read_file"})
	evts2 = append(evts2, flushToolSieve(&state, []string{"read_file"})...)

	var textContent string
	var toolCalls int
	allEvents := append(evts1, evts2...)
	for _, evt := range allEvents {
		if evt.Content != "" {
			textContent += evt.Content
		}
		toolCalls += len(evt.ToolCalls)
	}

	if strings.Contains(textContent, "<tool_call") {
		t.Fatalf("XML content leaked: %q", textContent)
	}
	if toolCalls == 0 {
		t.Fatal("expected tool calls to be extracted")
	}
}

func TestProcessToolSieveInterceptsAttemptCompletionLeak(t *testing.T) {
	var state toolStreamSieveState
	// Simulate an agent outputting attempt_completion XML tag 
	// which shouldn't leak to text output, even if it fails to parse as a valid tool.
	chunks := []string{
		"Done with task.\n",
		"<attempt_completion>\n",
		"  <result>Here is the answer</result>\n",
		"</attempt_completion>",
	}
	var events []toolStreamEvent
	for _, c := range chunks {
		events = append(events, processToolSieveChunk(&state, c, []string{"attempt_completion"})...)
	}
	events = append(events, flushToolSieve(&state, []string{"attempt_completion"})...)

	var textContent string
	for _, evt := range events {
		if evt.Content != "" {
			textContent += evt.Content
		}
	}

	if !strings.Contains(textContent, "Done with task.\n") {
		t.Fatalf("expected leading text to be emitted, got %q", textContent)
	}

	if strings.Contains(textContent, "<attempt_completion>") || strings.Contains(textContent, "result>") {
		t.Fatalf("agent XML tag content leaked to text: %q", textContent)
	}
}
