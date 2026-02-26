package agent

import (
	"fmt"
	"os"
)

func CalculateTokenUsage(ag Agent, transcriptData []byte, transcriptLinesAtStart int, subagentsDir string) *TokenUsage {
	if ag == nil {
		return nil
	}
	// Calculate token usage - prefer SubagentAwareExtractor to include subagent tokens
	var tokenUsage *TokenUsage
	if subagentExtractor, ok := ag.(SubagentAwareExtractor); ok {
		usage, tokenErr := subagentExtractor.CalculateTotalTokenUsage(transcriptData, transcriptLinesAtStart, subagentsDir)
		if tokenErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to calculate token usage (with subagents): %v\n", tokenErr)
		} else {
			tokenUsage = usage
		}
	} else if calculator, ok := ag.(TokenCalculator); ok {
		// Fall back to basic token calculation (main transcript only)
		usage, tokenErr := calculator.CalculateTokenUsage(transcriptData, transcriptLinesAtStart)
		if tokenErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to calculate token usage: %v\n", tokenErr)
		} else {
			tokenUsage = usage
		}
	}
	return tokenUsage
}
