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
	"time"

	"github.com/charmbracelet/huh"
	dispatchpkg "github.com/entireio/cli/cmd/entire/cli/dispatch"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	searchpkg "github.com/entireio/cli/cmd/entire/cli/search"
	"github.com/go-git/go-git/v6"
	"github.com/spf13/cobra"
)

var errDispatchCancelled = errors.New("dispatch cancelled")
var wizardNowUTC = func() time.Time { return time.Now().UTC() }
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
)

type dispatchWizardState struct {
	modeChoice       string
	scopeType        string
	timeWindowPreset string
	branchMode       string
	selectedRepos    []string
	selectedOrg      string
	voicePreset      string
	voiceCustom      string
	confirmRun       bool
}

type dispatchWizardChoices struct {
	currentRepo string
	repoOptions []huh.Option[string]
	orgOptions  []huh.Option[string]
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

func (s dispatchWizardState) sinceValue() string {
	return s.timeWindowPreset
}

func (s dispatchWizardState) voiceValue() string {
	switch strings.TrimSpace(s.voicePreset) {
	case "marvin":
		return "marvin"
	case "custom":
		if value := strings.TrimSpace(s.voiceCustom); value != "" {
			return value
		}
	}
	return "neutral"
}

func (s dispatchWizardState) showCustomVoiceInput() bool {
	return strings.TrimSpace(s.voicePreset) == "custom"
}

func (s dispatchWizardState) effectiveScopeType(choices dispatchWizardChoices) string {
	if s.isLocal() {
		return dispatchWizardScopeCurrentRepo
	}

	if s.scopeType == dispatchWizardScopeOrganization && len(choices.orgOptions) > 0 {
		return dispatchWizardScopeOrganization
	}
	if len(choices.repoOptions) > 0 {
		return dispatchWizardScopeSelectedRepos
	}
	if len(choices.orgOptions) > 0 {
		return dispatchWizardScopeOrganization
	}
	return dispatchWizardScopeSelectedRepos
}

func (s dispatchWizardState) effectiveBranchMode(choices dispatchWizardChoices) string {
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

func (s dispatchWizardState) selectedRepoPaths(choices dispatchWizardChoices) []string {
	if s.isLocal() || s.effectiveScopeType(choices) != dispatchWizardScopeSelectedRepos {
		return nil
	}
	return append([]string(nil), s.selectedRepos...)
}

func (s dispatchWizardState) showRepoPicker(choices dispatchWizardChoices) bool {
	return !s.isLocal() && s.effectiveScopeType(choices) == dispatchWizardScopeSelectedRepos
}

func (s dispatchWizardState) showScopePicker(choices dispatchWizardChoices) bool {
	return !s.isLocal() && len(choices.scopeOptions(s)) > 1
}

func (s dispatchWizardState) showOrganizationPicker(choices dispatchWizardChoices) bool {
	return !s.isLocal() && s.effectiveScopeType(choices) == dispatchWizardScopeOrganization
}

func (s dispatchWizardState) showBranchModePicker() bool {
	return true
}

func (s *dispatchWizardState) applyCurrentBranchDefault(branch string) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return
	}
	s.branchMode = dispatchWizardBranchCurrent
}

func (s dispatchWizardState) orgValue(choices dispatchWizardChoices) string {
	if s.isLocal() || s.effectiveScopeType(choices) != dispatchWizardScopeOrganization {
		return ""
	}
	if value := strings.TrimSpace(s.selectedOrg); value != "" {
		return value
	}
	if len(choices.orgOptions) > 0 {
		return choices.orgOptions[0].Value
	}
	return ""
}

func (s dispatchWizardState) resolve(choices dispatchWizardChoices, currentBranch func() (string, error)) (dispatchpkg.Options, error) {
	return resolveDispatchOptions(
		s.isLocal(),
		s.sinceValue(),
		"",
		s.effectiveBranchMode(choices) == dispatchWizardBranchAll,
		s.selectedRepoPaths(choices),
		s.orgValue(choices),
		s.voiceValue(),
		currentBranch,
	)
}

func (c dispatchWizardChoices) scopeOptions(state dispatchWizardState) []huh.Option[string] {
	if state.isLocal() {
		return []huh.Option[string]{huh.NewOption("Current repo", dispatchWizardScopeCurrentRepo)}
	}
	options := make([]huh.Option[string], 0, 2)
	if len(c.repoOptions) > 0 {
		options = append(options, huh.NewOption("Selected repos", dispatchWizardScopeSelectedRepos))
	}
	if len(c.orgOptions) > 0 {
		options = append(options, huh.NewOption("Organization", dispatchWizardScopeOrganization))
	}
	return options
}

func (c dispatchWizardChoices) branchModeOptions(state dispatchWizardState) []huh.Option[string] {
	if state.isLocal() {
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

func buildDispatchWizardSummary(opts dispatchpkg.Options) string {
	scope := "current repo"
	switch {
	case strings.TrimSpace(opts.Org) != "":
		scope = "org:" + strings.TrimSpace(opts.Org)
	case len(opts.RepoPaths) > 0:
		scope = "repos:" + strings.Join(opts.RepoPaths, ", ")
	}

	branches := "current branch"
	if opts.AllBranches {
		branches = "all"
	} else if opts.Mode == dispatchpkg.ModeLocal {
		branches = "current branch"
	} else if len(opts.Branches) > 0 {
		branches = strings.Join(opts.Branches, ", ")
	} else if strings.TrimSpace(opts.Org) != "" || len(opts.RepoPaths) > 0 {
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

func buildDispatchCommand(opts dispatchpkg.Options) string {
	return strings.Join(compactStrings([]string{
		"entire dispatch",
		mapBoolToFlag(opts.Mode == dispatchpkg.ModeLocal, "--local"),
		renderStringFlag("--since", strings.TrimSpace(opts.Since)),
		mapBoolToFlag(opts.AllBranches, "--all-branches"),
		renderStringFlag("--repos", strings.Join(opts.RepoPaths, ",")),
		renderStringFlag("--org", strings.TrimSpace(opts.Org)),
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
	choices, err := discoverDispatchWizardChoices(cmd.Context())
	if err != nil {
		return dispatchpkg.Options{}, err
	}

	state := newDispatchWizardState()
	if len(choices.repoOptions) > 0 {
		state.selectedRepos = []string{choices.repoOptions[0].Value}
	}
	if len(choices.orgOptions) > 0 {
		state.selectedOrg = choices.orgOptions[0].Value
	}

	currentBranch := func() (string, error) {
		return GetCurrentBranch(cmd.Context())
	}
	if branch, branchErr := currentBranch(); branchErr == nil {
		state.applyCurrentBranchDefault(branch)
	}

	form := NewAccessibleForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Dispatch mode").
				Options(
					huh.NewOption("Local", dispatchWizardModeLocal),
					huh.NewOption("Cloud", dispatchWizardModeServer),
				).
				Value(&state.modeChoice),
		).Title("Mode").Description("Choose where the dispatch should run."),
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Scope").
				OptionsFunc(func() []huh.Option[string] {
					return choices.scopeOptions(state)
				}, &state).
				Value(&state.scopeType),
		).Title("Scope").Description("Choose which cloud scope to dispatch.").
			WithHideFunc(func() bool {
				return !state.showScopePicker(choices)
			}),
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Repos").
				Description("Select repos to include.").
				Filterable(true).
				OptionsFunc(func() []huh.Option[string] {
					return append([]huh.Option[string](nil), choices.repoOptions...)
				}, nil).
				Value(&state.selectedRepos).
				Validate(func(value []string) error {
					if state.effectiveScopeType(choices) == dispatchWizardScopeSelectedRepos && len(value) == 0 {
						return errors.New("select at least one repo")
					}
					return nil
				}),
		).Title("Repos").Description("Choose which repos to include.").
			WithHideFunc(func() bool {
				return !state.showRepoPicker(choices)
			}),
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Organization").
				Description("Select the organization to dispatch.").
				OptionsFunc(func() []huh.Option[string] {
					if len(choices.orgOptions) == 0 {
						return []huh.Option[string]{huh.NewOption("No organizations discovered", "")}
					}
					return append([]huh.Option[string](nil), choices.orgOptions...)
				}, nil).
				Value(&state.selectedOrg).
				Validate(func(value string) error {
					if state.effectiveScopeType(choices) == dispatchWizardScopeOrganization && strings.TrimSpace(value) == "" {
						return errors.New("select an organization")
					}
					return nil
				}),
		).Title("Organization").Description("Choose which organization to include.").
			WithHideFunc(func() bool {
				return !state.showOrganizationPicker(choices)
			}),
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Time window").
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
				Title("Branch mode").
				OptionsFunc(func() []huh.Option[string] {
					return choices.branchModeOptions(state)
				}, &state).
				Value(&state.branchMode),
		).Title("Branch mode").Description("Choose how dispatch should interpret branch scope.").
			WithHideFunc(func() bool {
				return !state.showBranchModePicker()
			}),
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Voice").
				Options(
					huh.NewOption("Neutral", "neutral"),
					huh.NewOption("Marvin", "marvin"),
					huh.NewOption("Custom", "custom"),
				).
				Value(&state.voicePreset),
		).Title("Output").Description("Choose a preset voice."),
		huh.NewGroup(
			huh.NewInput().
				Title("Custom voice").
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
					opts, resolveErr := state.resolve(choices, currentBranch)
					if resolveErr != nil {
						return "Validation error: " + resolveErr.Error()
					}
					return buildDispatchWizardSummary(opts)
				}, &state),
			huh.NewNote().
				Title("Command").
				DescriptionFunc(func() string {
					opts, resolveErr := state.resolve(choices, currentBranch)
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

	if err := form.Run(); err != nil {
		if handled := handleFormCancellation(cmd.OutOrStdout(), "dispatch", err); handled == nil {
			return dispatchpkg.Options{}, errDispatchCancelled
		}
		return dispatchpkg.Options{}, err
	}
	if !state.confirmRun {
		fmt.Fprintln(cmd.OutOrStdout(), "dispatch cancelled.")
		return dispatchpkg.Options{}, errDispatchCancelled
	}

	return state.resolve(choices, currentBranch)
}

func discoverDispatchWizardChoices(ctx context.Context) (dispatchWizardChoices, error) {
	currentRepo, err := paths.WorktreeRoot(ctx)
	if err != nil {
		return dispatchWizardChoices{}, fmt.Errorf("not in a git repository: %w", err)
	}

	repoSlugs, err := listDispatchWizardRepos(ctx)
	if err != nil || len(repoSlugs) == 0 {
		repoSlugs = discoverLocalRepoSlugs(ctx, currentRepo)
	}
	sort.Strings(repoSlugs)

	repoOptions := make([]huh.Option[string], 0, len(repoSlugs))
	seenRepoSlugs := make(map[string]struct{}, len(repoSlugs))
	for _, repoSlug := range repoSlugs {
		if repoSlug == "" {
			continue
		}
		if _, ok := seenRepoSlugs[repoSlug]; ok {
			continue
		}
		seenRepoSlugs[repoSlug] = struct{}{}
		repoOptions = append(repoOptions, huh.NewOption(repoSlug, repoSlug))
	}

	orgNames, err := listDispatchWizardOrgs(ctx)
	if err != nil {
		orgNames = nil
	}
	sort.Strings(orgNames)

	orgOptions := make([]huh.Option[string], 0, len(orgNames))
	for _, org := range orgNames {
		orgOptions = append(orgOptions, huh.NewOption(org, org))
	}

	return dispatchWizardChoices{
		currentRepo: currentRepo,
		repoOptions: repoOptions,
		orgOptions:  orgOptions,
	}, nil
}

func discoverLocalRepoRoots(ctx context.Context, currentRepo string) []string {
	rootSet := map[string]struct{}{currentRepo: {}}
	parent := filepath.Dir(currentRepo)

	entries, err := os.ReadDir(parent)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			candidate := filepath.Join(parent, entry.Name())
			repoRoot, resolveErr := resolveGitTopLevel(ctx, candidate)
			if resolveErr != nil {
				continue
			}
			rootSet[repoRoot] = struct{}{}
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
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func discoverBranchOptions(ctx context.Context, repoRoot string) []huh.Option[string] {
	cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	branches := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !shouldHideDispatchWizardBranch(line) {
			branches = append(branches, line)
		}
	}
	sort.Strings(branches)

	options := make([]huh.Option[string], 0, len(branches))
	for _, branch := range branches {
		options = append(options, huh.NewOption(branch, branch))
	}
	return options
}

func shouldHideDispatchWizardBranch(branch string) bool {
	branch = strings.TrimSpace(branch)
	return strings.HasPrefix(branch, "entire/")
}

func discoverAuthenticatedDispatchWizardOrgs(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "gh", "api", "user/orgs", "--jq", ".[].login")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
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
		return nil, err
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
