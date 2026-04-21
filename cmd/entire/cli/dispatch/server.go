package dispatch

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/go-git/go-git/v6"
)

func runServer(ctx context.Context, opts Options) (*Dispatch, error) {
	token, err := lookupCurrentToken()
	if err != nil {
		return nil, fmt.Errorf("reading credentials: %w", err)
	}
	if token == "" {
		return nil, fmt.Errorf("dispatch requires login — run `entire login`")
	}

	now := nowUTC()
	sinceInput := strings.TrimSpace(opts.Since)
	if sinceInput == "" {
		sinceInput = "7d"
	}
	since, err := ParseSinceAtNow(sinceInput, now)
	if err != nil {
		return nil, err
	}
	until, err := ParseUntilAtNow(opts.Until, now)
	if err != nil {
		return nil, err
	}
	if !since.Before(until) {
		return nil, fmt.Errorf("--since must be before --until")
	}

	var repoScope any
	repoFullNames := append([]string(nil), opts.RepoPaths...)
	if opts.Org == "" && len(repoFullNames) == 0 {
		repoRoot, err := paths.WorktreeRoot(ctx)
		if err != nil {
			return nil, fmt.Errorf("not in a git repository: %w", err)
		}
		repo, err := git.PlainOpenWithOptions(repoRoot, &git.PlainOpenOptions{DetectDotGit: true})
		if err != nil {
			return nil, fmt.Errorf("open repository: %w", err)
		}
		repoFullName, err := resolveRepoFullName(repo)
		if err != nil {
			return nil, err
		}
		repoScope = repoFullName
	} else if len(repoFullNames) == 1 {
		repoScope = repoFullNames[0]
	} else if len(repoFullNames) > 1 {
		repoScope = repoFullNames
	}

	branches := any(opts.Branches)
	if opts.AllBranches {
		branches = "all"
	}

	cloud := NewCloudClient(CloudConfig{BaseURL: cloudBaseURL(), Token: token})
	reqBody := CreateDispatchRequest{
		Repo:     repoScope,
		Org:      opts.Org,
		Since:    since.Format(time.RFC3339),
		Until:    until.Format(time.RFC3339),
		Branches: branches,
		Generate: true,
		Voice:    opts.Voice,
	}
	response, err := cloud.CreateDispatch(ctx, reqBody)
	if err != nil {
		return nil, err
	}

	dispatch := apiToDispatch(response)
	dispatch.RequestedGenerate = opts.Generate
	if strings.TrimSpace(dispatch.GeneratedText) == "" {
		return nil, errDispatchMissingMarkdown
	}
	return dispatch, nil
}

func apiToDispatch(response *CreateDispatchResponse) *Dispatch {
	if response == nil {
		return &Dispatch{}
	}

	repos := make([]RepoGroup, 0, len(response.Repos))
	for _, repo := range response.Repos {
		sections := make([]Section, 0, len(repo.Sections))
		for _, section := range repo.Sections {
			bullets := make([]Bullet, 0, len(section.Bullets))
			for _, bullet := range section.Bullets {
				bullets = append(bullets, Bullet{
					CheckpointID: bullet.CheckpointID,
					Text:         bullet.Text,
					Source:       bullet.Source,
					Branch:       bullet.Branch,
					CreatedAt:    parseAPITime(bullet.CreatedAt),
					Labels:       append([]string(nil), bullet.Labels...),
				})
			}
			sections = append(sections, Section{
				Label:   section.Label,
				Bullets: bullets,
			})
		}
		repos = append(repos, RepoGroup{
			FullName: repo.FullName,
			Sections: sections,
		})
	}

	generatedText := strings.TrimSpace(response.GeneratedMarkdown)
	if generatedText == "" {
		generatedText = strings.TrimSpace(response.GeneratedText)
	}

	return &Dispatch{
		Window: Window{
			NormalizedSince:   parseAPITime(response.Window.NormalizedSince),
			NormalizedUntil:   parseAPITime(response.Window.NormalizedUntil),
			FirstCheckpointAt: parseAPITime(response.Window.FirstCheckpointCreatedAt),
			LastCheckpointAt:  parseAPITime(response.Window.LastCheckpointCreatedAt),
		},
		CoveredRepos:  append([]string(nil), response.CoveredRepos...),
		Repos:         repos,
		GeneratedText: generatedText,
		Generated:     generatedText != "",
		Totals: Totals{
			Checkpoints:         response.Totals.Checkpoints,
			UsedCheckpointCount: response.Totals.UsedCheckpointCount,
			Branches:            response.Totals.Branches,
			FilesTouched:        response.Totals.FilesTouched,
		},
		Warnings: Warnings{
			AccessDeniedCount:  response.Warnings.AccessDeniedCount,
			PendingCount:       response.Warnings.PendingCount,
			FailedCount:        response.Warnings.FailedCount,
			UnknownCount:       response.Warnings.UnknownCount,
			UncategorizedCount: response.Warnings.UncategorizedCount,
		},
	}
}

func parseAPITime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
