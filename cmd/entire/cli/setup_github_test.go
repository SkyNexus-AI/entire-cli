package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const testUser = "octocat"

func TestSlugifyRepoName(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"my-project":          "my-project",
		"My Cool Project":     "My-Cool-Project",
		"weird@@@name!!":      "weird-name",
		"":                    "my-repo",
		"---":                 "my-repo",
		"foo__bar":            "foo__bar",
		"a.b.c":               "a.b.c",
		"leading space":       "leading-space",
		"double  space  here": "double-space-here",
	}
	for in, want := range cases {
		if got := slugifyRepoName(in); got != want {
			t.Errorf("slugifyRepoName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateRepoName(t *testing.T) {
	t.Parallel()
	valid := []string{"my-repo", "foo_bar", "a.b.c", "Repo123", "x"}
	for _, name := range valid {
		if err := validateRepoName(name); err != nil {
			t.Errorf("validateRepoName(%q) unexpectedly returned error: %v", name, err)
		}
	}
	invalid := []string{"", "-leading", ".leading", "has/slash", "has space", strings.Repeat("a", 101)}
	for _, name := range invalid {
		if err := validateRepoName(name); err == nil {
			t.Errorf("validateRepoName(%q) = nil, want error", name)
		}
	}
}

// fakeRunner is a test seam for bootstrapRunner. Each (name, args[0]) pair
// maps to a response.
type fakeRunner struct {
	mu          sync.Mutex
	responses   map[string]fakeResponse
	interactive map[string]error
	calls       []fakeCall
}

type fakeResponse struct {
	stdout string
	err    error
}

type fakeCall struct {
	dir  string
	name string
	args []string
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{
		responses:   make(map[string]fakeResponse),
		interactive: make(map[string]error),
	}
}

func (f *fakeRunner) key(name string, args []string) string {
	return name + " " + strings.Join(args, " ")
}

func (f *fakeRunner) set(name string, args []string, stdout string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses[f.key(name, args)] = fakeResponse{stdout: stdout, err: err}
}

func (f *fakeRunner) setInteractive(name string, args []string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.interactive[f.key(name, args)] = err
}

func (f *fakeRunner) lookup(name string, args []string) (fakeResponse, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.responses[f.key(name, args)]
	return r, ok
}

func (f *fakeRunner) record(dir, name string, args []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fakeCall{dir: dir, name: name, args: args})
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	f.record("", name, args)
	if r, ok := f.lookup(name, args); ok {
		return r.stdout, r.err
	}
	return "", fmt.Errorf("fakeRunner: unexpected call %s %v", name, args)
}

func (f *fakeRunner) RunInDir(_ context.Context, dir, name string, args ...string) (string, error) {
	f.record(dir, name, args)
	if r, ok := f.lookup(name, args); ok {
		return r.stdout, r.err
	}
	return "", fmt.Errorf("fakeRunner: unexpected call in %s: %s %v", dir, name, args)
}

func (f *fakeRunner) RunInteractive(_ context.Context, dir, name string, args ...string) error {
	f.record(dir, name, args)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.interactive[f.key(name, args)]
}

// hasCall returns whether any recorded call matches the predicate.
func (f *fakeRunner) hasCall(match func(fakeCall) bool) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if match(c) {
			return true
		}
	}
	return false
}

func TestGhHelpers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newFakeRunner()

	r.set("gh", []string{"--version"}, "gh version 2.81.0\n", nil)
	r.set("gh", []string{"auth", "status"}, "Logged in", nil)
	r.set("gh", []string{"api", "user", "--jq", ".login"}, "octocat\n", nil)
	r.set("gh", []string{"api", "user/orgs", "--jq", ".[].login"}, "gamma\nalpha\n\nbeta\n", nil)

	if !ghAvailable(ctx, r) {
		t.Fatal("ghAvailable should be true")
	}
	if !ghAuthenticated(ctx, r) {
		t.Fatal("ghAuthenticated should be true")
	}
	user, err := ghCurrentUser(ctx, r)
	if err != nil || user != testUser {
		t.Fatalf("ghCurrentUser = %q, %v; want octocat", user, err)
	}
	orgs, err := ghListOrgs(ctx, r)
	if err != nil {
		t.Fatalf("ghListOrgs error: %v", err)
	}
	// Must be sorted, trimmed, and blank-skipped.
	want := []string{"alpha", "beta", "gamma"}
	if len(orgs) != len(want) {
		t.Fatalf("orgs = %v, want %v", orgs, want)
	}
	for i, o := range orgs {
		if o != want[i] {
			t.Fatalf("orgs[%d] = %q, want %q", i, o, want[i])
		}
	}
}

func TestGhAvailable_Missing(t *testing.T) {
	t.Parallel()
	r := newFakeRunner()
	r.set("gh", []string{"--version"}, "", errors.New("not found"))
	if ghAvailable(context.Background(), r) {
		t.Fatal("expected ghAvailable to return false when gh is missing")
	}
}

func TestResolveOwner_FlagAcceptsUnknown(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	owner, err := resolveOwner(&buf, testUser, []string{"acme"}, GitHubBootstrapOptions{RepoOwner: "external-org"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if owner != "external-org" {
		t.Fatalf("owner = %q, want external-org", owner)
	}
}

func TestResolveOwner_SingleDefault(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	owner, err := resolveOwner(&buf, testUser, nil, GitHubBootstrapOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if owner != testUser {
		t.Fatalf("owner = %q, want octocat", owner)
	}
	if !strings.Contains(buf.String(), testUser) {
		t.Fatalf("expected owner announcement, got %q", buf.String())
	}
}

func TestResolveVisibility_FlagInternalRequiresOrg(t *testing.T) {
	t.Parallel()
	_, err := resolveVisibility(io.Discard, testUser, testUser, GitHubBootstrapOptions{RepoVisibility: "internal"})
	if err == nil {
		t.Fatal("expected error for internal visibility on user repo")
	}
}

func TestResolveVisibility_FlagValid(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"public", "private", "internal"} {
		owner := testUser
		current := testUser
		if v == "internal" {
			owner = "acme"
		}
		got, err := resolveVisibility(io.Discard, owner, current, GitHubBootstrapOptions{RepoVisibility: v})
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", v, err)
		}
		if got != v {
			t.Fatalf("%s: got %q", v, got)
		}
	}
}

func TestResolveVisibility_FlagInvalid(t *testing.T) {
	t.Parallel()
	_, err := resolveVisibility(io.Discard, testUser, testUser, GitHubBootstrapOptions{RepoVisibility: "weird"})
	if err == nil {
		t.Fatal("expected error for invalid visibility")
	}
}

func TestResolveRepoName_FlagValidates(t *testing.T) {
	t.Parallel()
	r := newFakeRunner()
	// Return a non-ExitError; ghRepoExists then bubbles up, and resolveRepoName
	// logs a warning but proceeds with the flag-supplied name.
	r.set("gh", []string{"repo", "view", "octocat/ok-name", "--json", "name"}, "", errors.New("transient"))
	name, err := resolveRepoName(context.Background(), io.Discard, io.Discard, r, testUser, t.TempDir(), GitHubBootstrapOptions{RepoName: "ok-name"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "ok-name" {
		t.Fatalf("name = %q", name)
	}
}

func TestResolveRepoName_FlagRejectsInvalid(t *testing.T) {
	t.Parallel()
	r := newFakeRunner()
	_, err := resolveRepoName(context.Background(), io.Discard, io.Discard, r, testUser, t.TempDir(), GitHubBootstrapOptions{RepoName: "has/slash"})
	if err == nil {
		t.Fatal("expected error for name containing '/'")
	}
}

func TestGhRepoExists_RealErrorPath(t *testing.T) {
	t.Parallel()
	// If `gh repo view` succeeds (no error), the repo exists.
	r := newFakeRunner()
	r.set("gh", []string{"repo", "view", "octocat/real", "--json", "name"}, "{\"name\":\"real\"}", nil)
	exists, err := ghRepoExists(context.Background(), r, testUser, "real")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !exists {
		t.Fatal("expected exists=true")
	}
}

func TestDoInitialCommit_EmptyFolder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := newFakeRunner()
	r.set("git", []string{"add", "-A"}, "", nil)
	r.set("git", []string{"status", "--porcelain"}, "", nil)

	committed, err := doInitialCommit(context.Background(), r, dir, "msg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if committed {
		t.Fatal("expected committed=false for empty folder")
	}
}

func TestDoInitialCommit_WithFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := newFakeRunner()
	r.set("git", []string{"add", "-A"}, "", nil)
	r.set("git", []string{"status", "--porcelain"}, " M README.md\n", nil)
	r.set("git", []string{"commit", "-m", "msg"}, "", nil)

	committed, err := doInitialCommit(context.Background(), r, dir, "msg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !committed {
		t.Fatal("expected committed=true")
	}
}

func TestRunGitHubBootstrap_DeclinedInNonInteractive(t *testing.T) {
	t.Setenv("ENTIRE_TEST_TTY", "0")
	dir := t.TempDir()
	restoreCwd(t, dir)

	err := runGitHubBootstrapWith(context.Background(), io.Discard, io.Discard, GitHubBootstrapOptions{}, newFakeRunner())
	if !errors.Is(err, errBootstrapDeclined) {
		t.Fatalf("expected errBootstrapDeclined, got %v", err)
	}
}

func TestRunGitHubBootstrap_NoGitHubFlow(t *testing.T) {
	t.Setenv("ENTIRE_TEST_TTY", "0")
	dir := t.TempDir()
	restoreCwd(t, dir)

	r := newFakeRunner()
	r.set("git", []string{"init"}, "", nil)
	r.set("git", []string{"add", "-A"}, "", nil)
	r.set("git", []string{"status", "--porcelain"}, " M file\n", nil)
	r.set("git", []string{"commit", "-m", "First!"}, "", nil)

	opts := GitHubBootstrapOptions{
		InitRepo:             true,
		NoGitHub:             true,
		InitialCommitMessage: "First!",
	}
	err := runGitHubBootstrapWith(context.Background(), io.Discard, io.Discard, opts, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify git init ran in the cwd.
	if !r.hasCall(func(c fakeCall) bool {
		return c.name == "git" && len(c.args) == 1 && c.args[0] == "init"
	}) {
		t.Fatal("expected git init call")
	}
	// Verify no gh calls were made.
	if r.hasCall(func(c fakeCall) bool { return c.name == "gh" }) {
		t.Fatal("did not expect gh calls with --no-github")
	}
}

func TestRunGitHubBootstrap_GhMissingFallsBackToLocal(t *testing.T) {
	t.Setenv("ENTIRE_TEST_TTY", "0")
	dir := t.TempDir()
	restoreCwd(t, dir)

	r := newFakeRunner()
	r.set("gh", []string{"--version"}, "", errors.New("not found"))
	r.set("git", []string{"init"}, "", nil)
	r.set("git", []string{"add", "-A"}, "", nil)
	r.set("git", []string{"status", "--porcelain"}, "", nil)

	opts := GitHubBootstrapOptions{InitRepo: true}
	var errBuf bytes.Buffer
	err := runGitHubBootstrapWith(context.Background(), io.Discard, &errBuf, opts, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(errBuf.String(), "gh CLI not found") {
		t.Fatalf("expected hint about installing gh, got %q", errBuf.String())
	}
}

func TestRunGitHubBootstrap_FullNonInteractive(t *testing.T) {
	t.Setenv("ENTIRE_TEST_TTY", "0")
	dir := t.TempDir()
	restoreCwd(t, dir)

	r := newFakeRunner()
	r.set("gh", []string{"--version"}, "gh 2.81.0", nil)
	r.set("gh", []string{"auth", "status"}, "Logged in", nil)
	r.set("gh", []string{"api", "user", "--jq", ".login"}, "octocat\n", nil)
	r.set("gh", []string{"api", "user/orgs", "--jq", ".[].login"}, "", nil)
	// Name availability check: repo does not exist yet.
	r.set("gh", []string{"repo", "view", "octocat/my-new", "--json", "name"}, "", errors.New("not found"))
	r.set("git", []string{"init"}, "", nil)
	r.set("git", []string{"add", "-A"}, "", nil)
	r.set("git", []string{"status", "--porcelain"}, " M f\n", nil)
	r.set("git", []string{"commit", "-m", "Seed"}, "", nil)
	r.setInteractive("gh", []string{
		"repo", "create", "octocat/my-new",
		"--private",
		"--source=.",
		"--remote=origin",
		"--push",
	}, nil)

	opts := GitHubBootstrapOptions{
		InitRepo:             true,
		RepoName:             "my-new",
		RepoVisibility:       "private",
		InitialCommitMessage: "Seed",
	}
	err := runGitHubBootstrapWith(context.Background(), io.Discard, io.Discard, opts, r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !r.hasCall(func(c fakeCall) bool {
		return c.name == "gh" && len(c.args) > 3 && c.args[0] == "repo" && c.args[1] == "create"
	}) {
		t.Fatal("expected gh repo create call")
	}
}

func TestRunGitHubBootstrap_RepoExistsFails(t *testing.T) {
	t.Setenv("ENTIRE_TEST_TTY", "0")
	dir := t.TempDir()
	restoreCwd(t, dir)

	r := newFakeRunner()
	r.set("gh", []string{"--version"}, "gh", nil)
	r.set("gh", []string{"auth", "status"}, "", nil)
	r.set("gh", []string{"api", "user", "--jq", ".login"}, "octocat\n", nil)
	r.set("gh", []string{"api", "user/orgs", "--jq", ".[].login"}, "", nil)
	// The name is already taken. Since we aren't returning an *exec.ExitError,
	// ghRepoExists returns (false, err) and ghRepoExists wraps. To avoid
	// plumbing ExitError into the test, use the "already exists" path directly
	// by returning success — meaning the repo was found.
	r.set("gh", []string{"repo", "view", "octocat/taken", "--json", "name"}, "{\"name\":\"taken\"}", nil)
	r.set("git", []string{"init"}, "", nil)

	opts := GitHubBootstrapOptions{
		InitRepo: true,
		RepoName: "taken",
	}
	err := runGitHubBootstrapWith(context.Background(), io.Discard, io.Discard, opts, r)
	if err == nil {
		t.Fatal("expected error when repo already exists")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected 'already exists' error, got %v", err)
	}
}

// restoreCwd chdirs into dir for the duration of the test.
func restoreCwd(t *testing.T, dir string) {
	t.Helper()
	// macOS resolves /tmp → /private/tmp; canonicalize for safety.
	canon, err := filepath.EvalSymlinks(dir)
	if err != nil {
		canon = dir
	}
	t.Chdir(canon)
}
