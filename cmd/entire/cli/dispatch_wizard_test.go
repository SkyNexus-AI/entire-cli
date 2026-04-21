package cli

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
	dispatchpkg "github.com/entireio/cli/cmd/entire/cli/dispatch"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

func TestNewDispatchWizardState_Defaults(t *testing.T) {
	t.Parallel()

	state := newDispatchWizardState()
	if state.modeChoice != dispatchWizardModeLocal {
		t.Fatalf("expected local mode default, got %q", state.modeChoice)
	}
	if state.scopeType != dispatchWizardScopeCurrentRepo {
		t.Fatalf("expected current repo scope default, got %q", state.scopeType)
	}
	if state.timeWindowPreset != "7d" {
		t.Fatalf("expected 7d default, got %q", state.timeWindowPreset)
	}
	if state.branchMode != dispatchWizardBranchCurrent {
		t.Fatalf("expected current-branch mode default, got %q", state.branchMode)
	}
	if state.voicePreset != "neutral" {
		t.Fatalf("expected neutral voice preset default, got %q", state.voicePreset)
	}
	if state.voiceCustom != "" {
		t.Fatalf("expected empty custom voice default, got %q", state.voiceCustom)
	}
	if !state.confirmRun {
		t.Fatal("expected run confirmation to default to true")
	}
}

func TestDispatchWizardState_DefaultLocalBranchesPreselectCurrentBranch(t *testing.T) {
	t.Parallel()

	state := newDispatchWizardState()
	state.applyCurrentBranchDefault("feature/current")

	if got := state.branchMode; got != dispatchWizardBranchCurrent {
		t.Fatalf("expected current branch mode to remain selected, got %q", got)
	}
}

func TestDispatchWizardState_ResolveOrgDefaultsToDefaultBranches(t *testing.T) {
	t.Parallel()

	state := newDispatchWizardState()
	state.modeChoice = dispatchWizardModeServer
	state.scopeType = dispatchWizardScopeOrganization
	state.selectedOrg = "entireio"

	opts, err := state.resolve(dispatchWizardChoices{
		orgOptions: []huh.Option[string]{
			huh.NewOption("entireio", "entireio"),
		},
	}, func() (string, error) { return "feature/preview", nil })
	if err != nil {
		t.Fatal(err)
	}
	if opts.AllBranches {
		t.Fatal("did not expect org scope to default to all branches")
	}
	if opts.Branches != nil {
		t.Fatalf("expected nil branches, got %v", opts.Branches)
	}
}

func TestDispatchWizardState_ResolveAllBranches(t *testing.T) {
	t.Parallel()

	state := newDispatchWizardState()
	state.branchMode = dispatchWizardBranchAll

	opts, err := state.resolve(dispatchWizardChoices{}, func() (string, error) { return "feature/preview", nil })
	if err != nil {
		t.Fatal(err)
	}
	if !opts.AllBranches {
		t.Fatal("expected all branches")
	}
}

func TestDispatchWizardChoices_LocalBranchModes(t *testing.T) {
	t.Parallel()

	values := optionValues(dispatchWizardChoices{}.branchModeOptions(newDispatchWizardState()))
	if got := strings.Join(values, ","); got != dispatchWizardBranchCurrent+","+dispatchWizardBranchAll {
		t.Fatalf("unexpected local branch modes: %v", values)
	}
}

func TestDispatchWizardChoices_ServerBranchModes(t *testing.T) {
	t.Parallel()

	state := newDispatchWizardState()
	state.modeChoice = dispatchWizardModeServer

	values := optionValues(dispatchWizardChoices{}.branchModeOptions(state))
	if got := strings.Join(values, ","); got != dispatchWizardBranchDefault+","+dispatchWizardBranchAll {
		t.Fatalf("unexpected server branch modes: %v", values)
	}
}

func TestDispatchWizardChoices_ScopeOptionsAdaptByMode(t *testing.T) {
	t.Parallel()

	choices := dispatchWizardChoices{
		repoOptions: []huh.Option[string]{
			huh.NewOption("entireio/cli", "entireio/cli"),
			huh.NewOption("entireio/entire.io", "entireio/entire.io"),
		},
		orgOptions: []huh.Option[string]{
			huh.NewOption("entireio", "entireio"),
		},
	}

	state := newDispatchWizardState()
	localScopeValues := optionValues(choices.scopeOptions(state))
	if got := strings.Join(localScopeValues, ","); got != dispatchWizardScopeCurrentRepo {
		t.Fatalf("unexpected local scope options: %v", localScopeValues)
	}

	state.modeChoice = dispatchWizardModeServer
	serverScopeValues := optionValues(choices.scopeOptions(state))
	if got := strings.Join(serverScopeValues, ","); got != dispatchWizardScopeSelectedRepos+","+dispatchWizardScopeOrganization {
		t.Fatalf("unexpected server scope options: %v", serverScopeValues)
	}
}

func TestDispatchWizardChoices_ServerRepoOptionsUseFullSlugLabels(t *testing.T) {
	t.Parallel()

	choices := dispatchWizardChoices{
		repoOptions: []huh.Option[string]{
			huh.NewOption("entireio/cli", "entireio/cli"),
			huh.NewOption("entireio/entire.io", "entireio/entire.io"),
		},
	}

	if got := strings.Join(optionKeys(choices.repoOptions), ","); got != "entireio/cli,entireio/entire.io" {
		t.Fatalf("expected repo options to use org/repo labels, got %q", got)
	}
}

func TestDispatchWizardState_ServerModeKeepsSelectedReposScope(t *testing.T) {
	t.Parallel()

	state := newDispatchWizardState()
	state.modeChoice = dispatchWizardModeServer
	state.scopeType = dispatchWizardScopeSelectedRepos
	state.selectedRepos = []string{"entireio/cli"}

	if got := state.effectiveScopeType(dispatchWizardChoices{
		repoOptions: []huh.Option[string]{
			huh.NewOption("entireio/cli", "entireio/cli"),
		},
	}); got != dispatchWizardScopeSelectedRepos {
		t.Fatalf("expected server mode to keep selected repos scope, got %q", got)
	}

	opts, err := state.resolve(dispatchWizardChoices{
		repoOptions: []huh.Option[string]{
			huh.NewOption("entireio/cli", "entireio/cli"),
		},
	}, func() (string, error) { return "feature/preview", nil })
	if err != nil {
		t.Fatalf("expected server mode to resolve selected repos, got %v", err)
	}
	if got := strings.Join(opts.RepoPaths, ","); got != "entireio/cli" {
		t.Fatalf("expected selected repo path to propagate, got %q", got)
	}
}

func TestDispatchWizardState_ShowsRepoPickerOnlyForSelectedRepos(t *testing.T) {
	t.Parallel()

	state := newDispatchWizardState()
	choices := dispatchWizardChoices{}
	if state.showRepoPicker(choices) {
		t.Fatal("did not expect repo picker in local mode")
	}

	state.modeChoice = dispatchWizardModeServer
	state.scopeType = dispatchWizardScopeSelectedRepos
	if !state.showRepoPicker(choices) {
		t.Fatal("expected repo picker for selected repos scope in server mode")
	}
}

func TestDispatchWizardState_ShowsOrganizationPickerOnlyForOrgScope(t *testing.T) {
	t.Parallel()

	state := newDispatchWizardState()
	if state.showOrganizationPicker(dispatchWizardChoices{}) {
		t.Fatal("did not expect organization picker in local mode")
	}

	state.modeChoice = dispatchWizardModeServer
	if state.showOrganizationPicker(dispatchWizardChoices{}) {
		t.Fatal("did not expect organization picker for current repo server scope")
	}

	state.scopeType = dispatchWizardScopeOrganization
	if !state.showOrganizationPicker(dispatchWizardChoices{
		orgOptions: []huh.Option[string]{
			huh.NewOption("entireio", "entireio"),
		},
	}) {
		t.Fatal("expected organization picker for organization scope")
	}
}

func TestDispatchWizardState_ResolveVoiceInput(t *testing.T) {
	t.Parallel()

	state := newDispatchWizardState()
	state.voicePreset = "marvin"
	opts, err := state.resolve(dispatchWizardChoices{}, func() (string, error) { return "feature/preview", nil })
	if err != nil {
		t.Fatal(err)
	}
	if opts.Voice != "marvin" {
		t.Fatalf("expected marvin voice, got %q", opts.Voice)
	}

	state.voicePreset = "neutral"
	opts, err = state.resolve(dispatchWizardChoices{}, func() (string, error) { return "feature/preview", nil })
	if err != nil {
		t.Fatal(err)
	}
	if opts.Voice != "neutral" {
		t.Fatalf("expected neutral voice, got %q", opts.Voice)
	}
	state.voicePreset = "custom"
	state.voiceCustom = "dry, skeptical release note narrator"
	opts, err = state.resolve(dispatchWizardChoices{}, func() (string, error) { return "feature/preview", nil })
	if err != nil {
		t.Fatal(err)
	}
	if opts.Voice != "dry, skeptical release note narrator" {
		t.Fatalf("expected custom voice, got %q", opts.Voice)
	}
}

func TestDispatchWizardState_ResolveEmptyVoiceDefaultsToNeutral(t *testing.T) {
	t.Parallel()

	state := newDispatchWizardState()
	state.voicePreset = "custom"
	state.voiceCustom = "   "

	opts, err := state.resolve(dispatchWizardChoices{}, func() (string, error) { return "feature/preview", nil })
	if err != nil {
		t.Fatal(err)
	}
	if opts.Voice != "neutral" {
		t.Fatalf("expected neutral voice fallback, got %q", opts.Voice)
	}
}

func TestDispatchWizardState_ShowsCustomVoiceInputOnlyForCustomPreset(t *testing.T) {
	t.Parallel()

	state := newDispatchWizardState()
	if state.showCustomVoiceInput() {
		t.Fatal("did not expect custom voice input for default preset")
	}

	state.voicePreset = "custom"
	if !state.showCustomVoiceInput() {
		t.Fatal("expected custom voice input for custom preset")
	}
}

func TestBuildDispatchWizardSummary(t *testing.T) {
	t.Parallel()

	summary := buildDispatchWizardSummary(dispatchpkg.Options{
		Mode:        dispatchpkg.ModeLocal,
		RepoPaths:   []string{"/tmp/repo-a", "/tmp/repo-b"},
		Branches:    nil,
		AllBranches: false,
	})
	if !strings.Contains(summary, "Mode: local") {
		t.Fatalf("expected local mode in summary, got %q", summary)
	}
	if !strings.Contains(summary, "Scope: repos:/tmp/repo-a, /tmp/repo-b") {
		t.Fatalf("expected repo scope in summary, got %q", summary)
	}
	if !strings.Contains(summary, "Branches: current branch") {
		t.Fatalf("expected branches in summary, got %q", summary)
	}

	summary = buildDispatchWizardSummary(dispatchpkg.Options{
		Mode:        dispatchpkg.ModeServer,
		RepoPaths:   []string{"entireio/cli"},
		AllBranches: false,
	})
	if !strings.Contains(summary, "Mode: cloud") {
		t.Fatalf("expected cloud mode in summary, got %q", summary)
	}
}

func TestBuildDispatchCommand(t *testing.T) {
	t.Parallel()

	command := buildDispatchCommand(dispatchpkg.Options{
		Mode:        dispatchpkg.ModeServer,
		Since:       "7d",
		Branches:    nil,
		Voice:       "marvin",
		RepoPaths:   []string{"entireio/cli"},
		AllBranches: false,
	})
	if !strings.Contains(command, "entire dispatch") {
		t.Fatalf("expected base command, got %q", command)
	}
	if strings.Contains(command, "--generate") {
		t.Fatalf("did not expect generate flag, got %q", command)
	}
	if !strings.Contains(command, "--voice marvin") {
		t.Fatalf("expected preset voice flag, got %q", command)
	}
	if !strings.Contains(command, "--repos entireio/cli") {
		t.Fatalf("expected server repos flag, got %q", command)
	}
	if strings.Contains(command, "--local") {
		t.Fatalf("did not expect local flag, got %q", command)
	}
	if strings.Contains(command, "--branches") {
		t.Fatalf("did not expect branches flag for default-branch mode, got %q", command)
	}
	if strings.Contains(command, "--all-branches") {
		t.Fatalf("did not expect all-branches flag for default-branch mode, got %q", command)
	}
	if strings.Contains(command, "--dry-run") {
		t.Fatalf("did not expect dry-run flag, got %q", command)
	}
}

func TestBuildDispatchCommand_AllBranches(t *testing.T) {
	t.Parallel()

	command := buildDispatchCommand(dispatchpkg.Options{
		Mode:        dispatchpkg.ModeServer,
		Since:       "7d",
		Voice:       "marvin",
		RepoPaths:   []string{"entireio/cli"},
		AllBranches: true,
	})
	if !strings.Contains(command, "--all-branches") {
		t.Fatalf("expected all-branches flag, got %q", command)
	}
}

func TestDiscoverDispatchWizardChoices_UsesAuthenticatedOrgs(t *testing.T) {
	dir := t.TempDir()
	testutil.InitRepo(t, dir)
	testutil.WriteFile(t, dir, "a.txt", "x")
	testutil.GitAdd(t, dir, "a.txt")
	testutil.GitCommit(t, dir, "initial")
	addOriginRemote(t, dir, "https://github.com/entireio/cli.git")

	oldList := listDispatchWizardOrgs
	listDispatchWizardOrgs = func(context.Context) ([]string, error) {
		return []string{"beta", "alpha"}, nil
	}
	t.Cleanup(func() {
		listDispatchWizardOrgs = oldList
	})

	t.Chdir(dir)

	choices, err := discoverDispatchWizardChoices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(optionValues(choices.orgOptions), ","); got != "alpha,beta" {
		t.Fatalf("unexpected org options: %v", optionValues(choices.orgOptions))
	}
}

func TestDiscoverDispatchWizardChoices_UsesAuthenticatedRepos(t *testing.T) {
	dir := t.TempDir()
	testutil.InitRepo(t, dir)
	testutil.WriteFile(t, dir, "a.txt", "x")
	testutil.GitAdd(t, dir, "a.txt")
	testutil.GitCommit(t, dir, "initial")
	addOriginRemote(t, dir, "https://github.com/entireio/cli.git")

	oldListRepos := listDispatchWizardRepos
	oldListOrgs := listDispatchWizardOrgs
	listDispatchWizardRepos = func(context.Context) ([]string, error) {
		return []string{"entireio/entire.io", "entireio/cli", "entireio/cli"}, nil
	}
	listDispatchWizardOrgs = func(context.Context) ([]string, error) {
		return nil, nil
	}
	t.Cleanup(func() {
		listDispatchWizardRepos = oldListRepos
		listDispatchWizardOrgs = oldListOrgs
	})

	t.Chdir(dir)

	choices, err := discoverDispatchWizardChoices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(optionValues(choices.repoOptions), ","); got != "entireio/cli,entireio/entire.io" {
		t.Fatalf("unexpected repo options: %v", optionValues(choices.repoOptions))
	}
}

func TestDiscoverBranchOptions_HidesEntireBranchesKeepsClaudeBranches(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	testutil.InitRepo(t, dir)
	testutil.WriteFile(t, dir, "a.txt", "x")
	testutil.GitAdd(t, dir, "a.txt")
	testutil.GitCommit(t, dir, "initial")
	initialBranch := currentTestBranchName(t, dir)

	testutil.GitCheckoutNewBranch(t, dir, "claude/awesome-swanson")
	testutil.GitCheckoutNewBranch(t, dir, "feature/dispatch")
	testutil.CreateBranch(t, dir, "entire/checkpoints/v1")
	testutil.CreateBranch(t, dir, "entire/abc1234-8b2257")

	cmd := exec.Command("git", "checkout", initialBranch) //nolint:noctx // test helper
	cmd.Dir = dir
	cmd.Env = testutil.GitIsolatedEnv()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to checkout %s: %v\nOutput: %s", initialBranch, err, output)
	}

	options := discoverBranchOptions(context.Background(), dir)
	values := optionValues(options)
	got := strings.Join(values, ",")

	if strings.Contains(got, "entire/checkpoints/v1") {
		t.Fatalf("did not expect metadata branch in options: %v", values)
	}
	if strings.Contains(got, "entire/abc1234-8b2257") {
		t.Fatalf("did not expect shadow branch in options: %v", values)
	}
	if !strings.Contains(got, "claude/awesome-swanson") {
		t.Fatalf("expected claude branch to remain visible, got %v", values)
	}
	if !strings.Contains(got, "feature/dispatch") {
		t.Fatalf("expected normal branch to remain visible, got %v", values)
	}
}

func optionValues(options []huh.Option[string]) []string {
	values := make([]string, 0, len(options))
	for _, option := range options {
		values = append(values, option.Value)
	}
	return values
}

func currentTestBranchName(t *testing.T, repoDir string) string {
	t.Helper()

	cmd := exec.Command("git", "branch", "--show-current") //nolint:noctx // test helper
	cmd.Dir = repoDir
	cmd.Env = testutil.GitIsolatedEnv()
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to detect current branch: %v", err)
	}
	return strings.TrimSpace(string(output))
}

func addOriginRemote(t *testing.T, repoDir, remoteURL string) {
	t.Helper()

	cmd := exec.Command("git", "remote", "add", "origin", remoteURL) //nolint:noctx // test helper
	cmd.Dir = repoDir
	cmd.Env = testutil.GitIsolatedEnv()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to add origin remote: %v\nOutput: %s", err, output)
	}
}

func optionKeys(options []huh.Option[string]) []string {
	keys := make([]string, 0, len(options))
	for _, option := range options {
		keys = append(keys, option.Key)
	}
	return keys
}
