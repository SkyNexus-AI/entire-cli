package redact

import (
	"strings"
	"testing"
)

// highEntropySecret is a string with Shannon entropy > 4.5 that will trigger redaction.
const highEntropySecret = "sk-ant-api03-xK9mZ2vL8nQ5rT1wY4bC7dF0gH3jE6pA"

func TestBytes_NoSecrets(t *testing.T) {
	input := []byte("hello world, this is normal text")
	result := Bytes(input)
	if string(result) != string(input) {
		t.Errorf("expected unchanged input, got %q", result)
	}
	// Should return the original slice when no changes
	if &result[0] != &input[0] {
		t.Error("expected same underlying slice when no redaction needed")
	}
}

func TestBytes_WithSecret(t *testing.T) {
	input := []byte("my key is " + highEntropySecret + " ok")
	result := Bytes(input)
	if strings.Contains(string(result), highEntropySecret) {
		t.Error("expected secret to be redacted")
	}
	if !strings.Contains(string(result), "[REDACTED]") {
		t.Error("expected [REDACTED] placeholder")
	}
}

func TestJSONLBytes_NoSecrets(t *testing.T) {
	input := []byte(`{"type":"text","content":"hello"}`)
	result := JSONLBytes(input)
	if string(result) != string(input) {
		t.Errorf("expected unchanged input, got %q", result)
	}
	if &result[0] != &input[0] {
		t.Error("expected same underlying slice when no redaction needed")
	}
}

func TestJSONLBytes_WithSecret(t *testing.T) {
	input := []byte(`{"type":"text","content":"key=` + highEntropySecret + `"}`)
	result := JSONLBytes(input)
	if strings.Contains(string(result), highEntropySecret) {
		t.Error("expected secret to be redacted in JSONL content")
	}
	if !strings.Contains(string(result), "[REDACTED]") {
		t.Error("expected [REDACTED] placeholder in JSONL content")
	}
}

func TestCollectJSONLReplacements_UsesExportedString(t *testing.T) {
	// This test verifies the fix for the compiler error where
	// collectJSONLReplacements called redactString (unexported) instead of String.
	obj := map[string]any{
		"content": "token=" + highEntropySecret,
	}
	repls := collectJSONLReplacements(obj)
	if len(repls) == 0 {
		t.Fatal("expected at least one replacement for high-entropy secret")
	}
	if !strings.Contains(repls[0][1], "[REDACTED]") {
		t.Errorf("expected replacement to contain [REDACTED], got %q", repls[0][1])
	}
}
