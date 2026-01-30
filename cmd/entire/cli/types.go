package cli

import "entire.io/cli/cmd/entire/cli/transcript"

// Type aliases for backward compatibility with existing code in the cli package.
// These types are now defined in the transcript package.
type (
	transcriptLine   = transcript.Line
	userMessage      = transcript.UserMessage
	assistantMessage = transcript.AssistantMessage
	toolInput        = transcript.ToolInput
)
