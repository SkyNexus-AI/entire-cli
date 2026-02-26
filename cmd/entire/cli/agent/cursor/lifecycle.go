package cursor

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// ParseHookEvent translates a Cursor hook into a normalized lifecycle Event.
// Returns nil if the hook has no lifecycle significance.
func (c *CursorAgent) ParseHookEvent(_ context.Context, hookName string, stdin io.Reader) (*agent.Event, error) {
	switch hookName {
	case HookNameSessionStart:
		return c.parseSessionStart(stdin)
	case HookNameBeforeSubmitPrompt:
		return c.parseTurnStart(stdin)
	case HookNameStop:
		return c.parseTurnEnd(stdin)
	case HookNameSessionEnd:
		return c.parseSessionEnd(stdin)
	case HookNamePreCompact:
		return c.parsePreCompact(stdin)
	case HookNameSubagentStart:
		return c.parseSubagentStart(stdin)
	case HookNameSubagentStop:
		return c.parseSubagentStop(stdin)
	default:
		return nil, nil //nolint:nilnil // Unknown hooks have no lifecycle action
	}
}

// ReadTranscript reads the raw JSONL transcript bytes for a session.
func (c *CursorAgent) ReadTranscript(sessionRef string) ([]byte, error) {
	data, err := os.ReadFile(sessionRef) //nolint:gosec // Path comes from agent hook input
	if err != nil {
		return nil, fmt.Errorf("failed to read transcript: %w", err)
	}
	return data, nil
}

// Note: CursorAgent does NOT implement TranscriptAnalyzer. Cursor's transcript
// format does not contain tool_use blocks that would allow extracting modified
// files. File detection relies on git status instead.

// --- Internal hook parsing functions ---

func (c *CursorAgent) parseSessionStart(stdin io.Reader) (*agent.Event, error) {
	raw, err := agent.ReadAndParseHookInput[sessionInfoRaw](stdin)
	if err != nil {
		return nil, err
	}
	return &agent.Event{
		Type:       agent.SessionStart,
		SessionID:  raw.ConversationID,
		SessionRef: raw.TranscriptPath,
		Timestamp:  time.Now(),
	}, nil
}

func (c *CursorAgent) parseTurnStart(stdin io.Reader) (*agent.Event, error) {
	raw, err := agent.ReadAndParseHookInput[beforeSubmitPromptInputRaw](stdin)
	if err != nil {
		return nil, err
	}
	return &agent.Event{
		Type:       agent.TurnStart,
		SessionID:  raw.ConversationID,
		SessionRef: raw.TranscriptPath,
		Prompt:     raw.Prompt,
		Timestamp:  time.Now(),
	}, nil
}

func (c *CursorAgent) parseTurnEnd(stdin io.Reader) (*agent.Event, error) {
	raw, err := agent.ReadAndParseHookInput[sessionInfoRaw](stdin)
	if err != nil {
		return nil, err
	}
	return &agent.Event{
		Type:       agent.TurnEnd,
		SessionID:  raw.ConversationID,
		SessionRef: raw.TranscriptPath,
		Timestamp:  time.Now(),
	}, nil
}

func (c *CursorAgent) parseSessionEnd(stdin io.Reader) (*agent.Event, error) {
	raw, err := agent.ReadAndParseHookInput[sessionInfoRaw](stdin)
	if err != nil {
		return nil, err
	}
	return &agent.Event{
		Type:       agent.SessionEnd,
		SessionID:  raw.ConversationID,
		SessionRef: raw.TranscriptPath,
		Timestamp:  time.Now(),
	}, nil
}

func (c *CursorAgent) parsePreCompact(stdin io.Reader) (*agent.Event, error) {
	raw, err := agent.ReadAndParseHookInput[preCompactHookInputRaw](stdin)
	if err != nil {
		return nil, err
	}
	return &agent.Event{
		Type:       agent.Compaction,
		SessionID:  raw.ConversationID,
		SessionRef: raw.TranscriptPath,
		Timestamp:  time.Now(),
	}, nil
}

func (c *CursorAgent) parseSubagentStart(stdin io.Reader) (*agent.Event, error) {
	raw, err := agent.ReadAndParseHookInput[subagentStartHookInputRaw](stdin)
	if err != nil {
		return nil, err
	}
	if raw.Task == "" {
		return nil, nil //nolint:nilnil // nil event = no lifecycle action
	}
	return &agent.Event{
		Type:            agent.SubagentStart,
		SessionID:       raw.ConversationID,
		SessionRef:      raw.TranscriptPath,
		SubagentID:      raw.SubagentID,
		ToolUseID:       raw.SubagentID,
		SubagentType:    raw.SubagentType,
		TaskDescription: raw.Task,
		Timestamp:       time.Now(),
	}, nil
}

func (c *CursorAgent) parseSubagentStop(stdin io.Reader) (*agent.Event, error) {
	raw, err := agent.ReadAndParseHookInput[subagentStopHookInputRaw](stdin)
	if err != nil {
		return nil, err
	}
	if raw.Task == "" {
		return nil, nil //nolint:nilnil // nil event = no lifecycle action
	}
	event := &agent.Event{
		Type:            agent.SubagentEnd,
		SessionID:       raw.ConversationID,
		SessionRef:      raw.TranscriptPath,
		ToolUseID:       raw.SubagentID,
		SubagentType:    raw.SubagentType,
		TaskDescription: raw.Task,
		Timestamp:       time.Now(),
		SubagentID:      raw.SubagentID,
	}
	return event, nil
}
