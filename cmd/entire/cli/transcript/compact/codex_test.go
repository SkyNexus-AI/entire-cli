package compact

import "testing"

func TestCompact_CodexFixture(t *testing.T) {
	t.Parallel()

	codexOpts := agentOpts("codex")
	assertFixtureTransform(t, codexOpts, "testdata/codex_full.jsonl", "testdata/codex_expected.jsonl")
}

func TestCompact_CodexInlineCases(t *testing.T) {
	t.Parallel()

	codexOpts := agentOpts("codex")

	tests := []struct {
		name     string
		input    []byte
		expected []string
	}{
		{
			name: "user message with system content filtered",
			input: []byte(`{"timestamp":"t1","type":"session_meta","payload":{"id":"s1"}}
{"timestamp":"t2","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"<permissions>sandbox</permissions>"}]}}
{"timestamp":"t3","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md\ninstructions"},{"type":"input_text","text":"hello world"}]}}
`),
			expected: []string{
				`{"v":1,"agent":"codex","cli_version":"0.5.1","type":"user","ts":"t3","content":[{"text":"hello world"}]}`,
			},
		},
		{
			name: "assistant with function call and output",
			input: []byte(`{"timestamp":"t1","type":"session_meta","payload":{"id":"s1"}}
{"timestamp":"t2","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Running the command."}]}}
{"timestamp":"t3","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"ls\"}","call_id":"call_1"}}
{"timestamp":"t4","type":"response_item","payload":{"type":"function_call_output","call_id":"call_1","output":"file1.txt\nfile2.txt"}}
`),
			expected: []string{
				`{"v":1,"agent":"codex","cli_version":"0.5.1","type":"assistant","ts":"t2","content":[{"type":"text","text":"Running the command."},{"type":"tool_use","id":"call_1","name":"exec_command","input":{"cmd":"ls"},"result":{"output":"file1.txt\nfile2.txt","status":"success"}}]}`,
			},
		},
		{
			name: "drops reasoning and event_msg",
			input: []byte(`{"timestamp":"t1","type":"session_meta","payload":{"id":"s1"}}
{"timestamp":"t2","type":"event_msg","payload":{"type":"task_started"}}
{"timestamp":"t3","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}}
{"timestamp":"t4","type":"response_item","payload":{"type":"reasoning","summary":[]}}
{"timestamp":"t5","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}}
`),
			expected: []string{
				`{"v":1,"agent":"codex","cli_version":"0.5.1","type":"user","ts":"t3","content":[{"text":"hi"}]}`,
				`{"v":1,"agent":"codex","cli_version":"0.5.1","type":"assistant","ts":"t5","content":[{"type":"text","text":"hello"}]}`,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result, err := Compact(tc.input, codexOpts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertJSONLines(t, result, tc.expected)
		})
	}
}

func TestIsCodexFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input []byte
		want  bool
	}{
		{
			name:  "codex session_meta",
			input: []byte(`{"timestamp":"t1","type":"session_meta","payload":{"id":"s1"}}` + "\n"),
			want:  true,
		},
		{
			name:  "claude code format",
			input: []byte(`{"type":"user","uuid":"u1","timestamp":"t1","message":{"content":"hi"}}` + "\n"),
			want:  false,
		},
		{
			name:  "droid format",
			input: []byte(`{"type":"session_start","id":"s1"}` + "\n" + `{"type":"message","id":"m1","message":{"role":"user","content":"hi"}}` + "\n"),
			want:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isCodexFormat(tc.input); got != tc.want {
				t.Errorf("isCodexFormat() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCompact_CodexStartLine(t *testing.T) {
	t.Parallel()

	// StartLine skips the first N response_item entries (not raw JSONL lines).
	// There are 6 response_items here; StartLine=4 skips developer, AGENTS.md
	// user, first-prompt user, and first assistant — leaving second user + assistant.
	opts := MetadataFields{Agent: "codex", CLIVersion: "0.5.1", StartLine: 4}

	input := []byte(`{"timestamp":"t1","type":"session_meta","payload":{"id":"s1"}}
{"timestamp":"t2","type":"response_item","payload":{"type":"message","role":"developer","content":[{"type":"input_text","text":"<permissions>sandbox</permissions>"}]}}
{"timestamp":"t3","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md\ninstructions"}]}}
{"timestamp":"t4","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"first prompt"}]}}
{"timestamp":"t5","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"response to first"}]}}
{"timestamp":"t6","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"second prompt"}]}}
{"timestamp":"t7","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"response to second"}]}}
`)

	expected := []string{
		`{"v":1,"agent":"codex","cli_version":"0.5.1","type":"user","ts":"t6","content":[{"text":"second prompt"}]}`,
		`{"v":1,"agent":"codex","cli_version":"0.5.1","type":"assistant","ts":"t7","content":[{"type":"text","text":"response to second"}]}`,
	}

	result, err := Compact(input, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertJSONLines(t, result, expected)
}
