package transcript

import (
	"encoding/json"
	"testing"
)

func TestParseFromBytes_ValidJSONL(t *testing.T) {
	content := []byte(`{"type":"user","uuid":"u1","message":{"content":"hello"}}
{"type":"assistant","uuid":"a1","message":{"content":[{"type":"text","text":"hi"}]}}
`)

	lines, err := ParseFromBytes(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}

	if lines[0].Type != "user" || lines[0].UUID != "u1" {
		t.Errorf("unexpected first line: %+v", lines[0])
	}

	if lines[1].Type != "assistant" || lines[1].UUID != "a1" {
		t.Errorf("unexpected second line: %+v", lines[1])
	}
}

func TestParseFromBytes_EmptyContent(t *testing.T) {
	lines, err := ParseFromBytes([]byte{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(lines) != 0 {
		t.Errorf("expected 0 lines, got %d", len(lines))
	}
}

func TestParseFromBytes_MalformedLinesSkipped(t *testing.T) {
	content := []byte(`{"type":"user","uuid":"u1","message":{"content":"hello"}}
not valid json
{"type":"assistant","uuid":"a1","message":{"content":[{"type":"text","text":"hi"}]}}
`)

	lines, err := ParseFromBytes(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Malformed line should be skipped
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (skipping malformed), got %d", len(lines))
	}
}

func TestParseFromBytes_NoTrailingNewline(t *testing.T) {
	content := []byte(`{"type":"user","uuid":"u1","message":{"content":"hello"}}`)

	lines, err := ParseFromBytes(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
}

func TestExtractUserContent_StringContent(t *testing.T) {
	msg := UserMessage{Content: "Hello, world!"}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	content := ExtractUserContent(raw)

	if content != "Hello, world!" {
		t.Errorf("expected 'Hello, world!', got '%s'", content)
	}
}

func TestExtractUserContent_ArrayContent(t *testing.T) {
	// Array with text blocks
	raw := []byte(`{"content":[{"type":"text","text":"First part"},{"type":"text","text":"Second part"}]}`)

	content := ExtractUserContent(raw)

	expected := "First part\n\nSecond part"
	if content != expected {
		t.Errorf("expected '%s', got '%s'", expected, content)
	}
}

func TestExtractUserContent_EmptyMessage(t *testing.T) {
	content := ExtractUserContent([]byte(`{}`))

	if content != "" {
		t.Errorf("expected empty string, got '%s'", content)
	}
}

func TestExtractUserContent_InvalidJSON(t *testing.T) {
	content := ExtractUserContent([]byte(`not valid json`))

	if content != "" {
		t.Errorf("expected empty string for invalid JSON, got '%s'", content)
	}
}

func TestExtractUserContent_StripsIDETags(t *testing.T) {
	msg := UserMessage{Content: "<ide_opened_file>file.go</ide_opened_file>Hello, world!"}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	content := ExtractUserContent(raw)

	if content != "Hello, world!" {
		t.Errorf("expected IDE tags stripped, got '%s'", content)
	}
}

func TestExtractUserContent_ToolResultsIgnored(t *testing.T) {
	// Tool result content should return empty (not a user prompt)
	raw := []byte(`{"content":[{"type":"tool_result","tool_use_id":"123","content":"result"}]}`)

	content := ExtractUserContent(raw)

	if content != "" {
		t.Errorf("expected empty string for tool results, got '%s'", content)
	}
}
