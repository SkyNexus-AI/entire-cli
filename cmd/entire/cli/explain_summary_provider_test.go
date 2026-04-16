package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

type stubTextAgent struct {
	name   types.AgentName
	kind   types.AgentType
	absent bool
}

func (s *stubTextAgent) Name() types.AgentName { return s.name }
func (s *stubTextAgent) Type() types.AgentType { return s.kind }
func (s *stubTextAgent) Description() string   { return "stub" }
func (s *stubTextAgent) IsPreview() bool       { return false }
func (s *stubTextAgent) DetectPresence(context.Context) (bool, error) {
	return !s.absent, nil
}
func (s *stubTextAgent) ProtectedDirs() []string               { return nil }
func (s *stubTextAgent) ReadTranscript(string) ([]byte, error) { return nil, nil }
func (s *stubTextAgent) ChunkTranscript(context.Context, []byte, int) ([][]byte, error) {
	return nil, nil
}
func (s *stubTextAgent) ReassembleTranscript([][]byte) ([]byte, error) { return nil, nil }
func (s *stubTextAgent) GetSessionID(*agent.HookInput) string          { return "" }
func (s *stubTextAgent) GetSessionDir(string) (string, error)          { return "", nil }
func (s *stubTextAgent) ResolveSessionFile(string, string) string      { return "" }
func (s *stubTextAgent) ReadSession(*agent.HookInput) (*agent.AgentSession, error) {
	return nil, nil //nolint:nilnil // test stub
}
func (s *stubTextAgent) WriteSession(context.Context, *agent.AgentSession) error { return nil }
func (s *stubTextAgent) FormatResumeCommand(string) string                       { return "" }
func (s *stubTextAgent) GenerateText(context.Context, string, string) (string, error) {
	return `{"intent":"Intent","outcome":"Outcome","learnings":{"repo":[],"code":[],"workflow":[]},"friction":[],"open_items":[]}`, nil
}

func TestResolveCheckpointSummaryProvider_UsesConfiguredProvider(t *testing.T) {
	// Cannot use t.Parallel() because we use t.Chdir and package-level var stubs
	ctx := context.Background()
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	t.Chdir(tmpDir)

	originalLoad := loadSummarySettings
	originalGet := getSummaryAgent
	t.Cleanup(func() {
		loadSummarySettings = originalLoad
		getSummaryAgent = originalGet
	})

	loadSummarySettings = func(context.Context) (*settings.EntireSettings, error) {
		return &settings.EntireSettings{
			Enabled: true,
			SummaryGeneration: &settings.SummaryGenerationSettings{
				Provider: string(agent.AgentNameClaudeCode),
				Model:    "haiku",
			},
		}, nil
	}
	getSummaryAgent = func(name types.AgentName) (agent.Agent, error) {
		return &stubTextAgent{
			name: name,
			kind: agent.AgentTypeClaudeCode,
		}, nil
	}

	provider, err := resolveCheckpointSummaryProvider(ctx, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("resolveCheckpointSummaryProvider() error = %v", err)
	}

	if provider.Name != agent.AgentNameClaudeCode {
		t.Fatalf("provider.Name = %q, want %q", provider.Name, agent.AgentNameClaudeCode)
	}
	if provider.Model != "haiku" {
		t.Fatalf("provider.Model = %q, want %q", provider.Model, "haiku")
	}
}

func TestResolveCheckpointSummaryProvider_SavesSingleInstalledProvider(t *testing.T) {
	// Cannot use t.Parallel() because we use t.Chdir and package-level var stubs
	ctx := context.Background()
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	t.Chdir(tmpDir)

	originalLoad := loadSummarySettings
	originalGet := getSummaryAgent
	originalList := listRegisteredAgents
	t.Cleanup(func() {
		loadSummarySettings = originalLoad
		getSummaryAgent = originalGet
		listRegisteredAgents = originalList
	})

	loadSummarySettings = func(context.Context) (*settings.EntireSettings, error) {
		return &settings.EntireSettings{Enabled: true}, nil
	}
	listRegisteredAgents = func() []types.AgentName {
		return []types.AgentName{agent.AgentNameCodex}
	}
	getSummaryAgent = func(name types.AgentName) (agent.Agent, error) {
		return &stubTextAgent{
			name: name,
			kind: agent.AgentTypeCodex,
		}, nil
	}

	provider, err := resolveCheckpointSummaryProvider(ctx, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("resolveCheckpointSummaryProvider() error = %v", err)
	}
	if provider.Name != agent.AgentNameCodex {
		t.Fatalf("provider.Name = %q, want %q", provider.Name, agent.AgentNameCodex)
	}

	settingsPath := filepath.Join(tmpDir, ".entire", "settings.json")
	s, err := settings.LoadFromFile(settingsPath)
	if err != nil {
		t.Fatalf("LoadFromFile() error = %v", err)
	}
	if s.SummaryGeneration == nil {
		t.Fatal("expected summary_generation to be persisted")
	}
	if s.SummaryGeneration.Provider != string(agent.AgentNameCodex) {
		t.Fatalf("persisted provider = %q, want %q", s.SummaryGeneration.Provider, agent.AgentNameCodex)
	}
}

func TestResolveCheckpointSummaryProvider_NoCandidatesReturnsError(t *testing.T) {
	// Cannot use t.Parallel() because we use t.Chdir and package-level var stubs
	ctx := context.Background()
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	t.Chdir(tmpDir)

	originalLoad := loadSummarySettings
	originalGet := getSummaryAgent
	originalList := listRegisteredAgents
	t.Cleanup(func() {
		loadSummarySettings = originalLoad
		getSummaryAgent = originalGet
		listRegisteredAgents = originalList
	})

	loadSummarySettings = func(context.Context) (*settings.EntireSettings, error) {
		return &settings.EntireSettings{Enabled: true}, nil
	}
	listRegisteredAgents = func() []types.AgentName {
		return nil // no agents registered
	}
	getSummaryAgent = func(name types.AgentName) (agent.Agent, error) {
		return &stubTextAgent{name: name, kind: agent.AgentTypeClaudeCode}, nil
	}

	_, err := resolveCheckpointSummaryProvider(ctx, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error when no summary-capable CLI is installed")
	}
	if !strings.Contains(err.Error(), "no summary-capable agent CLI is installed") {
		t.Fatalf("unexpected error text: %v", err)
	}
}

func TestResolveCheckpointSummaryProvider_NonInteractiveMultiCandidatePicksFirst(t *testing.T) {
	// Cannot use t.Parallel() because we use t.Chdir, t.Setenv, and package-level var stubs
	ctx := context.Background()
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	t.Chdir(tmpDir)
	t.Setenv("ENTIRE_TEST_TTY", "0")

	originalLoad := loadSummarySettings
	originalGet := getSummaryAgent
	originalList := listRegisteredAgents
	t.Cleanup(func() {
		loadSummarySettings = originalLoad
		getSummaryAgent = originalGet
		listRegisteredAgents = originalList
	})

	loadSummarySettings = func(context.Context) (*settings.EntireSettings, error) {
		return &settings.EntireSettings{Enabled: true}, nil
	}
	listRegisteredAgents = func() []types.AgentName {
		// Deliberately omit Claude Code: the old behavior forced Claude
		// here even when not installed, which is the bug we're guarding
		// against.
		return []types.AgentName{agent.AgentNameCodex, agent.AgentNameGemini}
	}
	getSummaryAgent = func(name types.AgentName) (agent.Agent, error) {
		return &stubTextAgent{name: name, kind: agent.AgentTypeCodex}, nil
	}

	provider, err := resolveCheckpointSummaryProvider(ctx, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("resolveCheckpointSummaryProvider() error = %v", err)
	}
	if provider.Name != agent.AgentNameCodex {
		t.Fatalf("provider.Name = %q, want %q (first detected candidate, not Claude)", provider.Name, agent.AgentNameCodex)
	}
}

func TestResolveCheckpointSummaryProvider_ConfiguredProviderNotInstalledReturnsError(t *testing.T) {
	// Cannot use t.Parallel() because we use t.Chdir and package-level var stubs
	ctx := context.Background()
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	t.Chdir(tmpDir)

	originalLoad := loadSummarySettings
	originalGet := getSummaryAgent
	t.Cleanup(func() {
		loadSummarySettings = originalLoad
		getSummaryAgent = originalGet
	})

	loadSummarySettings = func(context.Context) (*settings.EntireSettings, error) {
		return &settings.EntireSettings{
			Enabled: true,
			SummaryGeneration: &settings.SummaryGenerationSettings{
				Provider: string(agent.AgentNameCodex),
			},
		}, nil
	}
	getSummaryAgent = func(name types.AgentName) (agent.Agent, error) {
		return &stubTextAgent{
			name:   name,
			kind:   agent.AgentTypeCodex,
			absent: true,
		}, nil
	}

	_, err := resolveCheckpointSummaryProvider(ctx, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error when configured provider's CLI is not installed")
	}
	if !strings.Contains(err.Error(), "not installed on this machine") {
		t.Fatalf("unexpected error text: %v", err)
	}
}

func TestFormatSummaryProviderDetails(t *testing.T) {
	t.Parallel()

	details := formatSummaryProviderDetails(&checkpointSummaryProvider{
		DisplayName: "Codex",
		Model:       "gpt-5",
	})

	if details != "Provider: Codex\nModel: gpt-5\n" {
		t.Fatalf("formatSummaryProviderDetails() = %q", details)
	}
}
