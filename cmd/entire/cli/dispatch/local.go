package dispatch

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/auth"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	checkpointid "github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/search"
	"github.com/entireio/cli/cmd/entire/cli/trailers"
	"github.com/go-git/go-git/v6"
	"golang.org/x/sync/errgroup"
)

var (
	lookupCurrentToken = auth.LookupCurrentToken
	nowUTC             = func() time.Time { return time.Now().UTC() }
)

func runLocal(ctx context.Context, opts Options) (*Dispatch, error) {
	if strings.TrimSpace(opts.Org) != "" {
		return nil, errors.New("--org cannot be used with --local")
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
	normalizedSince, normalizedUntil := NormalizeWindow(since, until)
	if !normalizedSince.Before(normalizedUntil) {
		return nil, errors.New("--since must be before --until")
	}

	repoRoots, err := resolveRepoRoots(ctx, opts.RepoPaths)
	if err != nil {
		return nil, err
	}

	allCandidates := make([]candidate, 0)
	var candidatesMu sync.Mutex
	group, groupCtx := errgroup.WithContext(ctx)
	for _, repoRoot := range repoRoots {
		repoRoot := repoRoot
		group.Go(func() error {
			candidates, err := enumerateRepoCandidates(groupCtx, repoRoot, opts, normalizedSince, normalizedUntil)
			if err != nil {
				return err
			}
			candidatesMu.Lock()
			allCandidates = append(allCandidates, candidates...)
			candidatesMu.Unlock()
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}

	fallback := applyFallbackChain(allCandidates)
	dispatch := &Dispatch{
		CoveredRepos: coveredRepos(allCandidates),
		Repos:        groupBulletsByRepo(fallback.Used),
		Totals:       computeTotals(allCandidates, fallback.Used),
		Warnings:     fallback.Warnings,
		Window: Window{
			NormalizedSince:   normalizedSince,
			NormalizedUntil:   normalizedUntil,
			FirstCheckpointAt: firstAt(fallback.Used),
			LastCheckpointAt:  lastAt(fallback.Used),
		},
		RequestedGenerate: opts.Generate,
	}

	text, err := generateLocalDispatch(ctx, dispatch, opts.Voice)
	if err != nil {
		return nil, err
	}
	dispatch.GeneratedText = text
	dispatch.Generated = strings.TrimSpace(text) != ""
	if !dispatch.Generated {
		return nil, errDispatchMissingMarkdown
	}

	return dispatch, nil
}

func NormalizeWindow(since, until time.Time) (time.Time, time.Time) {
	floored := since.Truncate(time.Minute)
	ceiled := until.Truncate(time.Minute)
	if !until.Equal(ceiled) {
		ceiled = ceiled.Add(time.Minute)
	}
	return floored, ceiled
}

func resolveRepoRoots(ctx context.Context, repoPaths []string) ([]string, error) {
	if len(repoPaths) == 0 {
		repoRoot, err := paths.WorktreeRoot(ctx)
		if err != nil {
			return nil, fmt.Errorf("not in a git repository: %w", err)
		}
		return []string{repoRoot}, nil
	}

	roots := make([]string, 0, len(repoPaths))
	for _, repoPath := range repoPaths {
		cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "rev-parse", "--show-toplevel")
		output, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("resolve repo root for %q: %w", repoPath, err)
		}
		roots = append(roots, strings.TrimSpace(string(output)))
	}
	return roots, nil
}

func enumerateRepoCandidates(ctx context.Context, repoRoot string, opts Options, since, until time.Time) ([]candidate, error) {
	repo, err := git.PlainOpenWithOptions(repoRoot, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil, fmt.Errorf("open repository %s: %w", repoRoot, err)
	}

	repoFullName, err := resolveRepoFullName(repo)
	if err != nil {
		return nil, fmt.Errorf("resolve repo name for %s: %w", repoRoot, err)
	}

	branches := opts.Branches
	if len(branches) == 0 && !opts.AllBranches {
		currentBranch, err := currentBranchName(repo)
		if err != nil {
			return nil, err
		}
		branches = []string{currentBranch}
	}
	branchSet := make(map[string]struct{}, len(branches))
	for _, branch := range branches {
		branchSet[branch] = struct{}{}
	}
	reachableCheckpointIDs := map[string]struct{}{}
	if opts.ImplicitCurrentBranch && !opts.AllBranches {
		reachableCheckpointIDs, err = reachableCheckpointIDsOnHEAD(ctx, repoRoot)
		if err != nil {
			return nil, err
		}
	}

	store := checkpoint.NewGitStore(repo)
	infos, err := store.ListCommitted(ctx)
	if err != nil {
		return nil, fmt.Errorf("list committed checkpoints: %w", err)
	}

	candidates := make([]candidate, 0, len(infos))
	for _, info := range infos {
		if info.CreatedAt.Before(since) || !info.CreatedAt.Before(until) {
			continue
		}

		summary, err := store.ReadCommitted(ctx, info.CheckpointID)
		if err != nil || summary == nil {
			continue
		}
		if !opts.AllBranches {
			if opts.ImplicitCurrentBranch {
				if _, ok := branchSet[summary.Branch]; ok {
					// The checkpoint was recorded on the current branch, so include it
					// even if HEAD no longer has a reachable trailer for that ID.
				} else if _, ok := reachableCheckpointIDs[info.CheckpointID.String()]; !ok {
					continue
				}
			} else {
				if _, ok := branchSet[summary.Branch]; !ok {
					continue
				}
			}
		}

		localSummary := ""
		if len(summary.Sessions) > 0 {
			latestIndex := len(summary.Sessions) - 1
			if metadata, err := store.ReadSessionMetadata(ctx, info.CheckpointID, latestIndex); err == nil && metadata != nil && metadata.Summary != nil {
				localSummary = strings.TrimSpace(metadata.Summary.Outcome)
				if localSummary == "" {
					localSummary = strings.TrimSpace(metadata.Summary.Intent)
				}
			}
		}

		commitSubject, _ := findCommitSubjectByCheckpoint(ctx, repoRoot, info.CheckpointID)
		candidates = append(candidates, candidate{
			CheckpointID:      info.CheckpointID.String(),
			RepoFullName:      repoFullName,
			Branch:            summary.Branch,
			CreatedAt:         info.CreatedAt,
			FilesTouched:      append([]string(nil), info.FilesTouched...),
			CommitSubject:     commitSubject,
			LocalSummaryTitle: localSummary,
		})
	}

	return candidates, nil
}

func reachableCheckpointIDsOnHEAD(ctx context.Context, repoRoot string) (map[string]struct{}, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "log", "HEAD", "--format=%B%x00")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list HEAD checkpoint trailers: %w", err)
	}

	reachable := make(map[string]struct{})
	for _, message := range strings.Split(string(output), "\x00") {
		for _, checkpointID := range trailers.ParseAllCheckpoints(message) {
			reachable[checkpointID.String()] = struct{}{}
		}
	}
	return reachable, nil
}

func resolveRepoFullName(repo *git.Repository) (string, error) {
	remote, err := repo.Remote("origin")
	if err != nil {
		return "", fmt.Errorf("find origin remote: %w", err)
	}
	if len(remote.Config().URLs) == 0 {
		return "", errors.New("origin remote has no URLs configured")
	}

	owner, repoName, err := search.ParseGitHubRemote(remote.Config().URLs[0])
	if err != nil {
		return "", err
	}
	return owner + "/" + repoName, nil
}

func currentBranchName(repo *git.Repository) (string, error) {
	head, err := repo.Head()
	if err != nil {
		return "", fmt.Errorf("get HEAD: %w", err)
	}
	if !head.Name().IsBranch() {
		return "", errors.New("not on a branch (detached HEAD)")
	}
	return head.Name().Short(), nil
}

func findCommitSubjectByCheckpoint(ctx context.Context, repoRoot string, checkpointID checkpointid.CheckpointID) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "log", "--all", "--format=%s%x00", "--grep", "Entire-Checkpoint: "+checkpointID.String())
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	parts := strings.SplitN(string(output), "\x00", 2)
	if len(parts) == 0 {
		return "", nil
	}
	return strings.TrimSpace(parts[0]), nil
}

func groupCandidatesByRepo(candidates []candidate) map[string][]string {
	grouped := make(map[string][]string)
	for _, candidate := range candidates {
		grouped[candidate.RepoFullName] = append(grouped[candidate.RepoFullName], candidate.CheckpointID)
	}
	return grouped
}

func coveredRepos(candidates []candidate) []string {
	if len(candidates) == 0 {
		return nil
	}

	repoSet := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate.RepoFullName == "" {
			continue
		}
		repoSet[candidate.RepoFullName] = struct{}{}
	}

	repos := make([]string, 0, len(repoSet))
	for repoFullName := range repoSet {
		repos = append(repos, repoFullName)
	}
	sort.Strings(repos)
	return repos
}

func groupBulletsByRepo(used []repoBullet) []RepoGroup {
	repoMap := make(map[string]map[string][]Bullet)
	for _, item := range used {
		if _, ok := repoMap[item.RepoFullName]; !ok {
			repoMap[item.RepoFullName] = make(map[string][]Bullet)
		}
		label := "Updates"
		if len(item.Bullet.Labels) > 0 && strings.TrimSpace(item.Bullet.Labels[0]) != "" {
			label = item.Bullet.Labels[0]
		}
		repoMap[item.RepoFullName][label] = append(repoMap[item.RepoFullName][label], item.Bullet)
	}

	repoNames := make([]string, 0, len(repoMap))
	for repoName := range repoMap {
		repoNames = append(repoNames, repoName)
	}
	sort.Strings(repoNames)

	out := make([]RepoGroup, 0, len(repoNames))
	for _, repoName := range repoNames {
		sectionMap := repoMap[repoName]
		labels := make([]string, 0, len(sectionMap))
		for label := range sectionMap {
			labels = append(labels, label)
		}
		sort.Strings(labels)

		sections := make([]Section, 0, len(labels))
		for _, label := range labels {
			sections = append(sections, Section{
				Label:   label,
				Bullets: sectionMap[label],
			})
		}
		out = append(out, RepoGroup{FullName: repoName, Sections: sections})
	}

	return out
}

func computeTotals(candidates []candidate, used []repoBullet) Totals {
	branches := make(map[string]struct{})
	filesTouched := make(map[string]struct{})
	for _, candidate := range candidates {
		if candidate.Branch != "" {
			branches[candidate.Branch] = struct{}{}
		}
		for _, file := range candidate.FilesTouched {
			filesTouched[file] = struct{}{}
		}
	}

	return Totals{
		Checkpoints:         len(candidates),
		UsedCheckpointCount: len(used),
		Branches:            len(branches),
		FilesTouched:        len(filesTouched),
	}
}

func firstAt(used []repoBullet) time.Time {
	var first time.Time
	for _, item := range used {
		if item.Bullet.CreatedAt.IsZero() {
			continue
		}
		if first.IsZero() || item.Bullet.CreatedAt.Before(first) {
			first = item.Bullet.CreatedAt
		}
	}
	return first
}

func lastAt(used []repoBullet) time.Time {
	var last time.Time
	for _, item := range used {
		if item.Bullet.CreatedAt.After(last) {
			last = item.Bullet.CreatedAt
		}
	}
	return last
}
