// Package summarise provides AI-powered summarisation of development sessions.
package summarise

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"entire.io/cli/cmd/entire/cli/checkpoint"
	"entire.io/cli/cmd/entire/cli/transcript"
)

// Generator generates checkpoint summaries using an LLM.
type Generator interface {
	// Generate creates a summary from checkpoint data.
	// Returns the generated summary or an error if generation fails.
	Generate(ctx context.Context, input Input) (*checkpoint.Summary, error)
}

// Input contains condensed checkpoint data for summarisation.
type Input struct {
	// Transcript is the condensed transcript entries
	Transcript []Entry

	// FilesTouched are the files modified during the session
	FilesTouched []string
}

// EntryType represents the type of a transcript entry.
type EntryType string

const (
	// EntryTypeUser indicates a user prompt entry.
	EntryTypeUser EntryType = "user"
	// EntryTypeAssistant indicates an assistant response entry.
	EntryTypeAssistant EntryType = "assistant"
	// EntryTypeTool indicates a tool call entry.
	EntryTypeTool EntryType = "tool"
)

// Entry represents one item in the condensed transcript.
type Entry struct {
	// Type is the entry type (user, assistant, tool)
	Type EntryType

	// Content is the text content for user/assistant entries
	Content string

	// ToolName is the name of the tool (for tool entries)
	ToolName string

	// ToolDetail is a description or file path (for tool entries)
	ToolDetail string
}

// BuildCondensedTranscriptFromBytes parses transcript bytes and extracts a condensed view.
// This is a convenience function that combines parsing and condensing.
func BuildCondensedTranscriptFromBytes(content []byte) ([]Entry, error) {
	lines, err := transcript.ParseFromBytes(content)
	if err != nil {
		return nil, fmt.Errorf("failed to parse transcript: %w", err)
	}
	return BuildCondensedTranscript(lines), nil
}

// BuildCondensedTranscript extracts a condensed view of the transcript.
// It processes user prompts, assistant responses, and tool calls into
// a simplified format suitable for LLM summarisation.
func BuildCondensedTranscript(lines []transcript.Line) []Entry {
	var entries []Entry

	for _, line := range lines {
		switch line.Type {
		case transcript.TypeUser:
			if entry := extractUserEntry(line); entry != nil {
				entries = append(entries, *entry)
			}
		case transcript.TypeAssistant:
			assistantEntries := extractAssistantEntries(line)
			entries = append(entries, assistantEntries...)
		}
	}

	return entries
}

// extractUserEntry extracts a user entry from a transcript line.
// Returns nil if the line doesn't contain a valid user prompt.
func extractUserEntry(line transcript.Line) *Entry {
	content := transcript.ExtractUserContent(line.Message)
	if content == "" {
		return nil
	}
	return &Entry{
		Type:    EntryTypeUser,
		Content: content,
	}
}

// extractAssistantEntries extracts assistant and tool entries from a transcript line.
func extractAssistantEntries(line transcript.Line) []Entry {
	var msg transcript.AssistantMessage
	if err := json.Unmarshal(line.Message, &msg); err != nil {
		return nil
	}

	var entries []Entry

	for _, block := range msg.Content {
		switch block.Type {
		case transcript.ContentTypeText:
			if block.Text != "" {
				entries = append(entries, Entry{
					Type:    EntryTypeAssistant,
					Content: block.Text,
				})
			}
		case transcript.ContentTypeToolUse:
			var input transcript.ToolInput
			_ = json.Unmarshal(block.Input, &input) //nolint:errcheck // Best-effort parsing

			detail := input.Description
			if detail == "" {
				detail = input.Command
			}
			if detail == "" {
				detail = input.FilePath
			}
			if detail == "" {
				detail = input.NotebookPath
			}
			if detail == "" {
				detail = input.Pattern
			}

			entries = append(entries, Entry{
				Type:       EntryTypeTool,
				ToolName:   block.Name,
				ToolDetail: detail,
			})
		}
	}

	return entries
}

// FormatCondensedTranscript formats an Input into a human-readable string for LLM.
// The format is:
//
//	[User] user prompt here
//
//	[Assistant] assistant response here
//
//	[Tool] ToolName: description or file path
func FormatCondensedTranscript(input Input) string {
	var sb strings.Builder

	for i, entry := range input.Transcript {
		if i > 0 {
			sb.WriteString("\n")
		}

		switch entry.Type {
		case EntryTypeUser:
			sb.WriteString("[User] ")
			sb.WriteString(entry.Content)
			sb.WriteString("\n")
		case EntryTypeAssistant:
			sb.WriteString("[Assistant] ")
			sb.WriteString(entry.Content)
			sb.WriteString("\n")
		case EntryTypeTool:
			sb.WriteString("[Tool] ")
			sb.WriteString(entry.ToolName)
			if entry.ToolDetail != "" {
				sb.WriteString(": ")
				sb.WriteString(entry.ToolDetail)
			}
			sb.WriteString("\n")
		}
	}

	if len(input.FilesTouched) > 0 {
		sb.WriteString("\n[Files Modified]\n")
		for _, file := range input.FilesTouched {
			fmt.Fprintf(&sb, "- %s\n", file)
		}
	}

	return sb.String()
}
