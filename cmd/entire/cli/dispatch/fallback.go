package dispatch

import (
	"strings"
	"time"
)

type candidate struct {
	CheckpointID      string
	RepoFullName      string
	Branch            string
	CreatedAt         time.Time
	FilesTouched      []string
	CommitSubject     string
	LocalSummaryTitle string
}

type repoBullet struct {
	RepoFullName string
	Bullet       Bullet
}

type fallbackResult struct {
	Used     []repoBullet
	Warnings Warnings
}

func applyFallbackChain(candidates []candidate) fallbackResult {
	result := fallbackResult{Used: make([]repoBullet, 0, len(candidates))}

	for _, candidate := range candidates {
		if text := strings.TrimSpace(candidate.LocalSummaryTitle); text != "" {
			result.Used = append(result.Used, repoBullet{
				RepoFullName: candidate.RepoFullName,
				Bullet: Bullet{
					CheckpointID: candidate.CheckpointID,
					Text:         text,
					Source:       "local_summary",
					Branch:       candidate.Branch,
					CreatedAt:    candidate.CreatedAt,
				},
			})
			continue
		}

		if text := strings.TrimSpace(candidate.CommitSubject); text != "" {
			result.Used = append(result.Used, repoBullet{
				RepoFullName: candidate.RepoFullName,
				Bullet: Bullet{
					CheckpointID: candidate.CheckpointID,
					Text:         text,
					Source:       "commit_message",
					Branch:       candidate.Branch,
					CreatedAt:    candidate.CreatedAt,
				},
			})
			continue
		}

		result.Warnings.UncategorizedCount++
	}

	return result
}
