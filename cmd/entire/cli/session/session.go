// Package session provides domain types and interfaces for managing AI coding sessions.
//
// A Session represents a unit of work with an AI agent (Claude Code, Cursor, etc.).
// Sessions can be nested - when a subagent runs, it creates a sub-session within
// the parent session.
//
// This package provides two levels of abstraction:
//
//  1. Sessions interface - High-level CRUD operations for full session objects,
//     combining session state with checkpoint data. This is the primary interface
//     for commands and the UI layer.
//
//  2. StateStore - Low-level primitive for managing session state files in
//     .git/entire-sessions/. This tracks active session state (base commit,
//     checkpoint count, etc.) but doesn't handle checkpoint content. Strategies
//     use this directly for performance-critical state management.
//
// See docs/architecture/sessions-and-checkpoints.md for the full domain model.
package session

import (
	"context"
	"time"

	"entire.io/cli/cmd/entire/cli/agent"
	"entire.io/cli/cmd/entire/cli/checkpoint"
)

// Session represents a unit of work with an AI coding agent.
// Sessions can be nested when subagents are used.
type Session struct {
	// ID is the unique session identifier
	ID string

	// FirstPrompt is the raw first user prompt (immutable)
	FirstPrompt string

	// Description is a human-readable summary (derived or editable)
	Description string

	// StartTime is when the session was started
	StartTime time.Time

	// AgentType identifies the AI agent (e.g., "Claude Code", "Cursor")
	AgentType agent.AgentType

	// AgentSessionID is the agent's internal session identifier
	AgentSessionID string

	// Checkpoints contains save points within this session
	Checkpoints []checkpoint.Checkpoint

	// SubSessions contains nested sessions from subagent work
	SubSessions []Session

	// ParentID is the parent session ID (empty for top-level sessions)
	ParentID string

	// ToolUseID is the tool invocation that spawned this sub-session
	// (empty for top-level sessions)
	ToolUseID string
}

// IsSubSession returns true if this session is a sub-session (has a parent).
func (s *Session) IsSubSession() bool {
	return s.ParentID != ""
}

// Sessions provides operations for managing sessions.
type Sessions interface {
	// Create creates a new session with the given options.
	Create(ctx context.Context, opts CreateSessionOptions) (*Session, error)

	// Get retrieves a session by ID.
	Get(ctx context.Context, sessionID string) (*Session, error)

	// List returns all top-level sessions (excludes sub-sessions).
	List(ctx context.Context) ([]Session, error)
}

// CreateSessionOptions contains parameters for creating a new session.
type CreateSessionOptions struct {
	// FirstPrompt is the initial user prompt
	FirstPrompt string

	// AgentType identifies the AI agent (e.g., "Claude Code", "Cursor")
	AgentType agent.AgentType

	// AgentSessionID is the agent's internal session identifier
	AgentSessionID string

	// ParentID is the parent session ID for sub-sessions (empty for top-level)
	ParentID string

	// ToolUseID is the tool invocation that spawned this sub-session
	// (empty for top-level sessions)
	ToolUseID string
}
