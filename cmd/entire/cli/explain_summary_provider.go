package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/interactive"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/summarize"

	"github.com/charmbracelet/huh"
)

var (
	loadSummarySettings         = LoadEntireSettings
	loadSummarySettingsFromFile = settings.LoadFromFile
	saveProjectSummarySettings  = SaveEntireSettings
	saveLocalSummarySettings    = SaveEntireSettingsLocal
	getSummaryAgent             = agent.Get
	listRegisteredAgents        = agent.List
)

type checkpointSummaryProvider struct {
	Name        types.AgentName
	DisplayName string
	Model       string
	Generator   summarize.Generator
}

func resolveCheckpointSummaryProvider(ctx context.Context, w io.Writer) (*checkpointSummaryProvider, error) {
	s, err := loadSummarySettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading settings: %w", err)
	}

	if s.SummaryGeneration != nil && s.SummaryGeneration.Provider != "" {
		providerName := types.AgentName(s.SummaryGeneration.Provider)
		if err := ensureSummaryProviderPresent(ctx, providerName); err != nil {
			return nil, err
		}
		return buildCheckpointSummaryProvider(providerName, s.SummaryGeneration.Model)
	}

	candidates := listEnabledSummaryProviders(ctx)

	switch len(candidates) {
	case 0:
		return nil, errors.New("no summary-capable agent CLI is installed on this machine; install one of claude, codex, gemini, cursor, or copilot, or set summary_generation.provider in settings")
	case 1:
		return autoSelectSummaryProvider(ctx, w, candidates[0].Name, "non-interactive auto-select: single installed provider")
	default:
		if !interactive.CanPromptInteractively() {
			return autoSelectSummaryProvider(ctx, w, candidates[0].Name, "non-interactive auto-select: first detected of multiple")
		}

		selected, err := promptForSummaryProvider(candidates)
		if err != nil {
			return nil, err
		}
		provider, err := autoSelectSummaryProvider(ctx, w, selected, "interactive prompt selection")
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(w, "Using %s for summary generation.\n", provider.DisplayName)
		return provider, nil
	}
}

// autoSelectSummaryProvider builds a provider for an auto-selected candidate
// (single-installed or non-interactive-first-of-many) and persists the choice
// so subsequent runs don't re-decide. Persistence failure is surfaced as a
// warning — not an error — because the selection is still usable in-process.
func autoSelectSummaryProvider(ctx context.Context, w io.Writer, name types.AgentName, reason string) (*checkpointSummaryProvider, error) {
	logging.Info(ctx, reason, "provider", string(name))
	provider, err := buildCheckpointSummaryProvider(name, "")
	if err != nil {
		return nil, err
	}
	if saveErr := persistSummaryProviderSelection(ctx, provider.Name, provider.Model); saveErr != nil {
		logging.Warn(ctx, "failed to save summary provider selection, continuing without persistence",
			"error", saveErr.Error())
		fmt.Fprintf(w, "Warning: could not save provider selection: %v\nUse `entire configure --summarize-provider %s` to set it manually.\n", saveErr, provider.Name)
	}
	return provider, nil
}

func listEnabledSummaryProviders(ctx context.Context) []checkpointSummaryProvider {
	registered := listRegisteredAgents()
	providers := make([]checkpointSummaryProvider, 0, len(registered))
	for _, name := range registered {
		ag, err := getSummaryAgent(name)
		if err != nil {
			continue
		}
		if _, ok := agent.AsTextGenerator(ag); !ok {
			continue
		}
		present, err := ag.DetectPresence(ctx)
		if err != nil {
			// Log at Debug so a broken install (e.g., permission error on the
			// agent's config dir) doesn't silently masquerade as "not installed"
			// without any trace.
			logging.Debug(ctx, "summary provider presence detection failed, skipping",
				"agent", string(name), "error", err.Error())
			continue
		}
		if !present {
			continue
		}
		providers = append(providers, checkpointSummaryProvider{
			Name:        name,
			DisplayName: string(ag.Type()),
		})
	}
	return providers
}

func promptForSummaryProvider(providers []checkpointSummaryProvider) (types.AgentName, error) {
	options := make([]huh.Option[string], 0, len(providers))
	for _, provider := range providers {
		options = append(options, huh.NewOption(provider.DisplayName, string(provider.Name)))
	}

	var selected string
	form := NewAccessibleForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Choose a summary provider").
				Description("This choice will be saved. Use `entire configure` to change it later.").
				Options(options...).
				Value(&selected),
		),
	)
	if err := form.Run(); err != nil {
		return "", fmt.Errorf("summary provider selection cancelled: %w", err)
	}

	return types.AgentName(selected), nil
}

func buildCheckpointSummaryProvider(name types.AgentName, model string) (*checkpointSummaryProvider, error) {
	ag, err := getSummaryAgent(name)
	if err != nil {
		return nil, fmt.Errorf("loading summary provider %s: %w", name, err)
	}

	textGenerator, ok := agent.AsTextGenerator(ag)
	if !ok {
		return nil, fmt.Errorf("agent %s does not support summary generation", name)
	}

	effectiveModel := summarize.ResolveModel(name, model)

	return &checkpointSummaryProvider{
		Name:        name,
		DisplayName: string(ag.Type()),
		Model:       effectiveModel,
		Generator: &summarize.TextGeneratorAdapter{
			TextGenerator: textGenerator,
			Model:         effectiveModel,
		},
	}, nil
}

// ensureSummaryProviderPresent returns an error if the named summary provider
// is registered but its CLI binary is not installed on this machine. Used to
// reject configured providers early (at resolve time) rather than deferring
// the failure to an opaque shell-out error during generation.
func ensureSummaryProviderPresent(ctx context.Context, name types.AgentName) error {
	ag, err := getSummaryAgent(name)
	if err != nil {
		return fmt.Errorf("loading summary provider %s: %w", name, err)
	}
	present, err := ag.DetectPresence(ctx)
	if err != nil {
		return fmt.Errorf("checking availability of summary provider %s: %w", name, err)
	}
	if !present {
		return fmt.Errorf("summary provider %q is configured but its CLI is not installed on this machine; install it or update summary_generation.provider in settings", name)
	}
	return nil
}

func validateSummaryProvider(provider string) error {
	ag, err := getSummaryAgent(types.AgentName(provider))
	if err != nil {
		return fmt.Errorf("unknown summary provider %q: %w", provider, err)
	}
	if _, ok := agent.AsTextGenerator(ag); !ok {
		return fmt.Errorf("agent %q does not support summary generation", provider)
	}
	return nil
}

func persistSummaryProviderSelection(ctx context.Context, provider types.AgentName, model string) error {
	targetFile, _ := settingsTargetFile(ctx, false, false)
	targetFileAbs, err := paths.AbsPath(ctx, targetFile)
	if err != nil {
		targetFileAbs = targetFile
	}

	s, err := loadSummarySettingsFromFile(targetFileAbs)
	if err != nil {
		return fmt.Errorf("loading settings for update: %w", err)
	}
	if s.SummaryGeneration == nil {
		s.SummaryGeneration = &settings.SummaryGenerationSettings{}
	}
	s.SummaryGeneration.SetProvider(string(provider), model)

	if targetFile == settings.EntireSettingsLocalFile {
		if err := saveLocalSummarySettings(ctx, s); err != nil {
			return fmt.Errorf("saving summary provider selection: %w", err)
		}
		return nil
	}
	if err := saveProjectSummarySettings(ctx, s); err != nil {
		return fmt.Errorf("saving summary provider selection: %w", err)
	}
	return nil
}

func formatSummaryProviderDetails(provider *checkpointSummaryProvider) string {
	if provider == nil {
		return ""
	}
	displayModel := provider.Model
	if displayModel == "" {
		displayModel = "provider default"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Provider: %s\n", provider.DisplayName)
	fmt.Fprintf(&b, "Model: %s\n", displayModel)
	return b.String()
}
