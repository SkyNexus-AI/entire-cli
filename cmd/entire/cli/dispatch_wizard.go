package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/charmbracelet/huh"
	dispatchpkg "github.com/entireio/cli/cmd/entire/cli/dispatch"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	searchpkg "github.com/entireio/cli/cmd/entire/cli/search"
	"github.com/go-git/go-git/v6"
	"github.com/spf13/cobra"
)

var errDispatchCancelled = errors.New("dispatch cancelled")
var listDispatchWizardRepos = discoverAuthenticatedDispatchWizardRepos
var listDispatchWizardOrgs = discoverAuthenticatedDispatchWizardOrgs

const (
	dispatchWizardModeLocal  = "local"
	dispatchWizardModeServer = "server"

	dispatchWizardScopeCurrentRepo   = "current_repo"
	dispatchWizardScopeSelectedRepos = "selected_repos"
	dispatchWizardScopeOrganization  = "organization"

	dispatchWizardBranchDefault = "default"
	dispatchWizardBranchCurrent = "current"
	dispatchWizardBranchAll     = "all"

	dispatchWizardVoiceCustom = "custom"
)

type dispatchWizardState struct {
	modeChoice       string
	scopeType        string
	timeWindowPreset string
	branchMode       string
	selectedRepos    []string
	selectedOrgs     []string
	voicePreset      string
	voiceCustom      string
	confirmRun       bool
}

func newDispatchWizardState() dispatchWizardState {
	return dispatchWizardState{
		modeChoice:       dispatchWizardModeLocal,
		scopeType:        dispatchWizardScopeCurrentRepo,
		timeWindowPreset: "7d",
		branchMode:       dispatchWizardBranchCurrent,
		voicePreset:      "neutral",
		confirmRun:       true,
	}
}

func (s dispatchWizardState) isLocal() bool {
	return s.modeChoice != dispatchWizardModeServer
}

func (s dispatchWizardState) voiceValue() string {
	switch strings.TrimSpace(s.voicePreset) {
	case "marvin":
		return "marvin"
	case dispatchWizardVoiceCustom:
		if value := strings.TrimSpace(s.voiceCustom); value != "" {
			return value
		}
	}
	return "neutral"
}

func (s dispatchWizardState) showCustomVoiceInput() bool {
	return strings.TrimSpace(s.voicePreset) == dispatchWizardVoiceCustom
}

func (s dispatchWizardState) effectiveScopeType() string {
	if s.isLocal() {
		return dispatchWizardScopeCurrentRepo
	}
	if s.scopeType == dispatchWizardScopeOrganization {
		return dispatchWizardScopeOrganization
	}
	return dispatchWizardScopeSelectedRepos
}

func (s dispatchWizardState) effectiveBranchMode() string {
	if s.isLocal() {
		if s.branchMode == dispatchWizardBranchAll {
			return dispatchWizardBranchAll
		}
		return dispatchWizardBranchCurrent
	}

	switch s.branchMode {
	case dispatchWizardBranchDefault, dispatchWizardBranchAll:
	default:
		return dispatchWizardBranchDefault
	}
	return s.branchMode
}

func (s dispatchWizardState) selectedReposList() []string {
	return normalizeDispatchWizardSelections(s.selectedRepos)
}

func (s dispatchWizardState) selectedOrganizations() []string {
	return normalizeDispatchWizardSelections(s.selectedOrgs)
}

func (s dispatchWizardState) resolveCloudScope() ([]string, []string) {
	if s.isLocal() {
		return nil, nil
	}

	switch s.effectiveScopeType() {
	case dispatchWizardScopeSelectedRepos:
		return s.selectedReposList(), nil
	case dispatchWizardScopeOrganization:
		return nil, s.selectedOrganizations()
	default:
		return nil, nil
	}
}

func (s dispatchWizardState) showRepoPicker() bool {
	return !s.isLocal() && s.effectiveScopeType() == dispatchWizardScopeSelectedRepos
}

func (s dispatchWizardState) showScopePicker() bool {
	return !s.isLocal()
}

func (s dispatchWizardState) showOrganizationPicker() bool {
	return !s.isLocal() && s.effectiveScopeType() == dispatchWizardScopeOrganization
}

func (s dispatchWizardState) resolve(currentBranch func() (string, error)) (dispatchpkg.Options, error) {
	repoPaths, orgs := s.resolveCloudScope()
	opts, err := resolveDispatchOptions(
		s.isLocal(),
		s.timeWindowPreset,
		"",
		s.effectiveBranchMode() == dispatchWizardBranchAll,
		repoPaths,
		orgs,
		s.voiceValue(),
		currentBranch,
	)
	if err != nil {
		return dispatchpkg.Options{}, err
	}
	return opts, nil
}

func (s dispatchWizardState) scopeOptions() []huh.Option[string] {
	if s.isLocal() {
		return []huh.Option[string]{huh.NewOption("Current repo", dispatchWizardScopeCurrentRepo)}
	}
	return []huh.Option[string]{
		huh.NewOption("Repos", dispatchWizardScopeSelectedRepos),
		huh.NewOption("Organizations", dispatchWizardScopeOrganization),
	}
}

func (s dispatchWizardState) branchModeOptions() []huh.Option[string] {
	if s.isLocal() {
		return []huh.Option[string]{
			huh.NewOption("Current branch", dispatchWizardBranchCurrent),
			huh.NewOption("All branches", dispatchWizardBranchAll),
		}
	}
	return []huh.Option[string]{
		huh.NewOption("Default branches", dispatchWizardBranchDefault),
		huh.NewOption("All branches", dispatchWizardBranchAll),
	}
}

func buildDispatchWizardSummary(opts dispatchpkg.Options, scope string) string {
	if strings.TrimSpace(scope) == "" {
		scope = resolvedDispatchScope(opts)
	}

	branches := "current branch"
	switch {
	case opts.AllBranches:
		branches = "all"
	case opts.Mode == dispatchpkg.ModeLocal:
		branches = "current branch"
	case len(opts.Branches) > 0:
		branches = strings.Join(opts.Branches, ", ")
	case len(opts.Orgs) > 0 || len(opts.RepoPaths) > 0:
		branches = "default branches"
	}

	mode := "cloud"
	if opts.Mode == dispatchpkg.ModeLocal {
		mode = "local"
	}

	return strings.Join([]string{
		"Mode: " + mode,
		"Scope: " + scope,
		"Branches: " + branches,
	}, "\n")
}

func resolvedDispatchScope(opts dispatchpkg.Options) string {
	scope := "current repo"
	switch {
	case len(opts.Orgs) == 1:
		scope = "org:" + opts.Orgs[0]
	case len(opts.Orgs) > 1:
		scope = "orgs:" + strings.Join(opts.Orgs, ", ")
	case len(opts.RepoPaths) > 0:
		scope = "repos:" + strings.Join(opts.RepoPaths, ", ")
	}
	return scope
}

func (s dispatchWizardState) previewScope(opts dispatchpkg.Options) string {
	if s.isLocal() {
		return resolvedDispatchScope(opts)
	}

	switch s.effectiveScopeType() {
	case dispatchWizardScopeSelectedRepos:
		selectedRepos := s.selectedReposList()
		if len(selectedRepos) > 0 {
			return "repos:" + strings.Join(selectedRepos, ", ")
		}
	case dispatchWizardScopeOrganization:
		selectedOrgs := s.selectedOrganizations()
		switch len(selectedOrgs) {
		case 1:
			return "org:" + selectedOrgs[0]
		case 2, 3, 4, 5:
			return "orgs:" + strings.Join(selectedOrgs, ", ")
		default:
			if len(selectedOrgs) > 0 {
				return fmt.Sprintf("orgs:%s (%d)", strings.Join(selectedOrgs, ", "), len(selectedOrgs))
			}
		}
	}

	return resolvedDispatchScope(opts)
}

func buildDispatchCommand(opts dispatchpkg.Options) string {
	return strings.Join(compactStrings([]string{
		"entire dispatch",
		mapBoolToFlag(opts.Mode == dispatchpkg.ModeLocal, "--local"),
		renderStringFlag("--since", strings.TrimSpace(opts.Since)),
		mapBoolToFlag(opts.AllBranches, "--all-branches"),
		renderStringFlag("--repos", strings.Join(opts.RepoPaths, ",")),
		renderStringFlag("--org", strings.Join(opts.Orgs, ",")),
		renderStringFlag("--voice", strings.TrimSpace(opts.Voice)),
	}), " ")
}

func compactStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func mapBoolToFlag(enabled bool, flag string) string {
	if enabled {
		return flag
	}
	return ""
}

func renderStringFlag(name string, value string) string {
	if value == "" {
		return ""
	}
	return name + " " + quoteShellValue(value)
}

func quoteShellValue(value string) string {
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, " ,:\t") {
		return fmt.Sprintf("%q", value)
	}
	return value
}

func runDispatchWizard(cmd *cobra.Command) (dispatchpkg.Options, error) {
	ctx := cmd.Context()

	currentRepo, err := paths.WorktreeRoot(ctx)
	if err != nil {
		return dispatchpkg.Options{}, fmt.Errorf("not in a git repository: %w", err)
	}

	loadRepos := newLazyOptions(func() []huh.Option[string] {
		slugs, listErr := listDispatchWizardRepos(ctx)
		if listErr != nil || len(slugs) == 0 {
			slugs = discoverLocalRepoSlugs(ctx, currentRepo)
		}
		return buildDispatchRepoOptions(slugs)
	})
	loadOrgs := newLazyOptions(func() []huh.Option[string] {
		names, listErr := listDispatchWizardOrgs(ctx)
		if listErr != nil || len(names) == 0 {
			return []huh.Option[string]{huh.NewOption("No organizations discovered", "")}
		}
		return buildDispatchOrgOptions(names)
	})

	// Prefetch in the background so the form opens immediately and Cloud users
	// are unlikely to block on the gh calls by the time they reach Repos/Org.
	go loadRepos()
	go loadOrgs()

	state := newDispatchWizardState()
	currentBranch := func() (string, error) {
		return GetCurrentBranch(ctx)
	}

	form := NewAccessibleForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Options(
					huh.NewOption("Local", dispatchWizardModeLocal),
					huh.NewOption("Cloud", dispatchWizardModeServer),
				).
				Value(&state.modeChoice),
		).Title("Mode").Description("Choose where the dispatch should run."),
		huh.NewGroup(
			huh.NewSelect[string]().
				OptionsFunc(func() []huh.Option[string] { //nolint:gocritic // method value would bind to stale state snapshot
					return state.scopeOptions()
				}, &state).
				Height(0).
				Value(&state.scopeType),
		).Title("Scope").Description("Choose which cloud scope to dispatch.").
			WithHideFunc(func() bool {
				return !state.showScopePicker()
			}),
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Repos").
				Description("Press / to filter.").
				Filterable(true).
				OptionsFunc(loadRepos, nil).
				Value(&state.selectedRepos).
				Validate(func(value []string) error {
					if state.effectiveScopeType() == dispatchWizardScopeSelectedRepos && len(normalizeDispatchWizardSelections(value)) == 0 {
						return errors.New("select at least one repo")
					}
					return nil
				}),
		).WithHideFunc(func() bool {
			return !state.showRepoPicker()
		}),
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Organizations").
				Description("Press / to filter.").
				Filterable(true).
				OptionsFunc(loadOrgs, nil).
				Value(&state.selectedOrgs).
				Validate(func(value []string) error {
					if state.effectiveScopeType() == dispatchWizardScopeOrganization && len(normalizeDispatchWizardSelections(value)) == 0 {
						return errors.New("select at least one organization")
					}
					return nil
				}),
		).Title("Organizations").Description("Choose which organizations to include.").
			WithHideFunc(func() bool {
				return !state.showOrganizationPicker()
			}),
		huh.NewGroup(
			huh.NewSelect[string]().
				Options(
					huh.NewOption("1 day", "1d"),
					huh.NewOption("7 days", "7d"),
					huh.NewOption("14 days", "14d"),
					huh.NewOption("30 days", "30d"),
				).
				Value(&state.timeWindowPreset),
		).Title("Window").Description("Choose the time window."),
		huh.NewGroup(
			huh.NewSelect[string]().
				OptionsFunc(func() []huh.Option[string] { //nolint:gocritic // method value would bind to stale state snapshot
					return state.branchModeOptions()
				}, &state).
				Height(0).
				Value(&state.branchMode),
		).Title("Branch mode").Description("Choose how dispatch should interpret branch scope."),
		huh.NewGroup(
			huh.NewSelect[string]().
				Options(
					huh.NewOption("Neutral", "neutral"),
					huh.NewOption("Marvin", "marvin"),
					huh.NewOption("Custom", dispatchWizardVoiceCustom),
				).
				Value(&state.voicePreset),
		).Title("Voice").Description("Choose a preset voice."),
		huh.NewGroup(
			huh.NewInput().
				Placeholder("Dry, skeptical release note narrator").
				Value(&state.voiceCustom).
				Validate(func(value string) error {
					if state.showCustomVoiceInput() && strings.TrimSpace(value) == "" {
						return errors.New("enter a custom voice")
					}
					return nil
				}),
		).Title("Custom voice").Description("Describe the dispatch voice.").
			WithHideFunc(func() bool {
				return !state.showCustomVoiceInput()
			}),
		huh.NewGroup(
			huh.NewNote().
				Title("Resolved options").
				DescriptionFunc(func() string {
					opts, resolveErr := state.resolve(currentBranch)
					if resolveErr != nil {
						return "Validation error: " + resolveErr.Error()
					}
					return buildDispatchWizardSummary(opts, state.previewScope(opts))
				}, &state),
			huh.NewNote().
				Title("Command").
				DescriptionFunc(func() string {
					opts, resolveErr := state.resolve(currentBranch)
					if resolveErr != nil {
						return "Validation error: " + resolveErr.Error()
					}
					return buildDispatchCommand(opts)
				}, &state),
			huh.NewConfirm().
				Title("Run dispatch?").
				Affirmative("Run").
				Negative("Cancel").
				Value(&state.confirmRun),
		).Title("Confirm").Description("Review the resolved command and run it."),
	)

	fmt.Fprintln(cmd.OutOrStdout())

	if err := form.Run(); err != nil {
		if handled := handleFormCancellation(cmd.OutOrStdout(), "dispatch", err); handled == nil {
			return dispatchpkg.Options{}, errDispatchCancelled
		}
		return dispatchpkg.Options{}, fmt.Errorf("run dispatch wizard: %w", err)
	}
	if !state.confirmRun {
		fmt.Fprintln(cmd.OutOrStdout(), "dispatch cancelled.")
		return dispatchpkg.Options{}, errDispatchCancelled
	}

	return state.resolve(currentBranch)
}

// newLazyOptions returns a func that runs loader once (under sync.Once) and
// returns the cached result on subsequent calls. Safe for concurrent use.
func newLazyOptions(loader func() []huh.Option[string]) func() []huh.Option[string] {
	var (
		once    sync.Once
		options []huh.Option[string]
	)
	return func() []huh.Option[string] {
		once.Do(func() {
			options = loader()
		})
		return options
	}
}

func buildDispatchRepoOptions(slugs []string) []huh.Option[string] {
	sort.Strings(slugs)
	options := make([]huh.Option[string], 0, len(slugs))
	seen := make(map[string]struct{}, len(slugs))
	for _, slug := range slugs {
		if slug == "" {
			continue
		}
		if _, ok := seen[slug]; ok {
			continue
		}
		seen[slug] = struct{}{}
		options = append(options, huh.NewOption(slug, slug))
	}
	return options
}

func buildDispatchOrgOptions(names []string) []huh.Option[string] {
	sort.Strings(names)
	options := make([]huh.Option[string], 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		options = append(options, huh.NewOption(name, name))
	}
	return options
}

func normalizeDispatchWizardSelections(values []string) []string {
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	return normalized
}

func discoverLocalRepoRoots(ctx context.Context, currentRepo string) []string {
	rootSet := map[string]struct{}{currentRepo: {}}
	parent := filepath.Dir(currentRepo)

	entries, err := os.ReadDir(parent)
	if err == nil {
		candidates := make([]string, 0, len(entries))
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			candidate := filepath.Join(parent, entry.Name())
			if _, statErr := os.Stat(filepath.Join(candidate, ".git")); statErr != nil {
				continue
			}
			candidates = append(candidates, candidate)
		}

		resolved := make([]string, len(candidates))
		var wg sync.WaitGroup
		wg.Add(len(candidates))
		for i, candidate := range candidates {
			go func() {
				defer wg.Done()
				if repoRoot, resolveErr := resolveGitTopLevel(ctx, candidate); resolveErr == nil {
					resolved[i] = repoRoot
				}
			}()
		}
		wg.Wait()
		for _, repoRoot := range resolved {
			if repoRoot != "" {
				rootSet[repoRoot] = struct{}{}
			}
		}
	}

	repoRoots := make([]string, 0, len(rootSet))
	for repoRoot := range rootSet {
		repoRoots = append(repoRoots, repoRoot)
	}
	sort.Slice(repoRoots, func(i, j int) bool {
		if repoRoots[i] == currentRepo {
			return true
		}
		if repoRoots[j] == currentRepo {
			return false
		}
		return filepath.Base(repoRoots[i]) < filepath.Base(repoRoots[j])
	})
	return repoRoots
}

func discoverLocalRepoSlugs(ctx context.Context, currentRepo string) []string {
	repoRoots := discoverLocalRepoRoots(ctx, currentRepo)
	repoSlugs := make([]string, 0, len(repoRoots))
	seenRepoSlugs := make(map[string]struct{}, len(repoRoots))
	for _, repoRoot := range repoRoots {
		repoSlug := discoverRepoSlug(repoRoot)
		if repoSlug == "" {
			continue
		}
		if _, ok := seenRepoSlugs[repoSlug]; ok {
			continue
		}
		seenRepoSlugs[repoSlug] = struct{}{}
		repoSlugs = append(repoSlugs, repoSlug)
	}
	return repoSlugs
}

func resolveGitTopLevel(ctx context.Context, path string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", path, "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

func discoverAuthenticatedDispatchWizardOrgs(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "gh", "api", "user/orgs", "--jq", ".[].login")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh api user/orgs: %w", err)
	}

	orgs := make([]string, 0)
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			orgs = append(orgs, line)
		}
	}
	sort.Strings(orgs)
	return orgs, nil
}

func discoverAuthenticatedDispatchWizardRepos(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(
		ctx,
		"gh",
		"api",
		"--paginate",
		"user/repos?per_page=100&affiliation=owner,collaborator,organization_member&sort=full_name",
		"--jq",
		".[].full_name",
	)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gh api user/repos: %w", err)
	}

	repos := make([]string, 0)
	seen := make(map[string]struct{})
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, ok := seen[line]; ok {
			continue
		}
		seen[line] = struct{}{}
		repos = append(repos, line)
	}
	sort.Strings(repos)
	return repos, nil
}

func discoverRepoSlug(repoRoot string) string {
	repo, err := git.PlainOpenWithOptions(repoRoot, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return ""
	}
	remote, err := repo.Remote("origin")
	if err != nil || len(remote.Config().URLs) == 0 {
		return ""
	}
	owner, repoName, err := searchpkg.ParseGitHubRemote(remote.Config().URLs[0])
	if err != nil {
		return ""
	}
	return owner + "/" + repoName
}
