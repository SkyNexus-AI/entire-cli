// Package settings provides configuration loading for Entire.
// This package is separate from cli to allow strategy package to import it
// without creating an import cycle (cli imports strategy).
package settings

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/entireio/cli/cmd/entire/cli/jsonutil"
	"github.com/entireio/cli/cmd/entire/cli/paths"
)

const (
	// EntireSettingsFile is the path to the Entire settings file
	EntireSettingsFile = ".entire/settings.json"
	// EntireSettingsLocalFile is the path to the local settings override file (not committed)
	EntireSettingsLocalFile = ".entire/settings.local.json"
)

// EntireSettings represents the .entire/settings.json configuration
type EntireSettings struct {

	// Enabled indicates whether Entire is active. When false, CLI commands
	// show a disabled message and hooks exit silently. Defaults to true.
	Enabled bool `json:"enabled"`

	// LocalDev indicates whether to use "go run" instead of the "entire" binary
	// This is used for development when the binary is not installed
	LocalDev bool `json:"local_dev,omitempty"`

	// LogLevel sets the logging verbosity (debug, info, warn, error).
	// Can be overridden by ENTIRE_LOG_LEVEL environment variable.
	// Defaults to "info".
	LogLevel string `json:"log_level,omitempty"`

	// StrategyOptions contains strategy-specific configuration
	StrategyOptions map[string]any `json:"strategy_options,omitempty"`

	// Telemetry controls anonymous usage analytics.
	// nil = not asked yet (show prompt), true = opted in, false = opted out
	Telemetry *bool `json:"telemetry,omitempty"`

	// Deprecated: no longer used. Exists to tolerate old settings files
	// that still contain "strategy": "auto-commit" or similar.
	Strategy string `json:"strategy,omitempty"`
}

// Load loads the Entire settings from .entire/settings.json,
// then applies any overrides from .entire/settings.local.json if it exists.
// Returns default settings if neither file exists.
// Works correctly from any subdirectory within the repository.
func Load(ctx context.Context) (*EntireSettings, error) {
	// Get absolute paths for settings files
	settingsFileAbs, err := paths.AbsPath(ctx, EntireSettingsFile)
	if err != nil {
		settingsFileAbs = EntireSettingsFile // Fallback to relative
	}
	localSettingsFileAbs, err := paths.AbsPath(ctx, EntireSettingsLocalFile)
	if err != nil {
		localSettingsFileAbs = EntireSettingsLocalFile // Fallback to relative
	}

	// Load base settings
	settings, err := loadFromFile(settingsFileAbs)
	if err != nil {
		return nil, fmt.Errorf("reading settings file: %w", err)
	}

	// Apply local overrides if they exist
	localData, err := os.ReadFile(localSettingsFileAbs) //nolint:gosec // path is from AbsPath or constant
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("reading local settings file: %w", err)
		}
		// Local file doesn't exist, continue without overrides
	} else {
		if err := mergeJSON(settings, localData); err != nil {
			return nil, fmt.Errorf("merging local settings: %w", err)
		}
	}

	return settings, nil
}

// LoadFromFile loads settings from a specific file path without merging local overrides.
// Returns default settings if the file doesn't exist.
// Use this when you need to display individual settings files separately.
func LoadFromFile(filePath string) (*EntireSettings, error) {
	return loadFromFile(filePath)
}

// loadFromFile loads settings from a specific file path.
// Returns default settings if the file doesn't exist.
func loadFromFile(filePath string) (*EntireSettings, error) {
	settings := &EntireSettings{
		Enabled: true, // Default to enabled
	}

	data, err := os.ReadFile(filePath) //nolint:gosec // path is from caller
	if err != nil {
		if os.IsNotExist(err) {
			return settings, nil
		}
		return nil, fmt.Errorf("%w", err)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(settings); err != nil {
		return nil, fmt.Errorf("parsing settings file: %w", err)
	}

	return settings, nil
}

// mergeJSON merges JSON data into existing settings.
// Only non-zero values from the JSON override existing settings.
func mergeJSON(settings *EntireSettings, data []byte) error {
	// First, validate that there are no unknown keys using strict decoding
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var temp EntireSettings
	if err := dec.Decode(&temp); err != nil {
		return fmt.Errorf("parsing JSON: %w", err)
	}

	// Parse into a map to check which fields are present
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("parsing JSON: %w", err)
	}

	// Override enabled if present
	if enabledRaw, ok := raw["enabled"]; ok {
		var e bool
		if err := json.Unmarshal(enabledRaw, &e); err != nil {
			return fmt.Errorf("parsing enabled field: %w", err)
		}
		settings.Enabled = e
	}

	// Override local_dev if present
	if localDevRaw, ok := raw["local_dev"]; ok {
		var ld bool
		if err := json.Unmarshal(localDevRaw, &ld); err != nil {
			return fmt.Errorf("parsing local_dev field: %w", err)
		}
		settings.LocalDev = ld
	}

	// Override log_level if present and non-empty
	if logLevelRaw, ok := raw["log_level"]; ok {
		var ll string
		if err := json.Unmarshal(logLevelRaw, &ll); err != nil {
			return fmt.Errorf("parsing log_level field: %w", err)
		}
		if ll != "" {
			settings.LogLevel = ll
		}
	}

	// Merge strategy_options if present
	if optionsRaw, ok := raw["strategy_options"]; ok {
		var opts map[string]any
		if err := json.Unmarshal(optionsRaw, &opts); err != nil {
			return fmt.Errorf("parsing strategy_options field: %w", err)
		}
		if settings.StrategyOptions == nil {
			settings.StrategyOptions = opts
		} else {
			for k, v := range opts {
				settings.StrategyOptions[k] = v
			}
		}
	}

	// Override telemetry if present
	if telemetryRaw, ok := raw["telemetry"]; ok {
		var t bool
		if err := json.Unmarshal(telemetryRaw, &t); err != nil {
			return fmt.Errorf("parsing telemetry field: %w", err)
		}
		settings.Telemetry = &t
	}

	return nil
}

// IsSetUp returns true if Entire has been set up in the current repository.
// This checks if .entire/settings.json exists.
// Use this to avoid creating files/directories in repos where Entire was never enabled.
func IsSetUp(ctx context.Context) bool {
	settingsFileAbs, err := paths.AbsPath(ctx, EntireSettingsFile)
	if err != nil {
		return false
	}
	_, err = os.Stat(settingsFileAbs)
	return err == nil
}

// IsSetUpAndEnabled returns true if Entire is both set up and enabled.
// This checks if .entire/settings.json exists AND has enabled: true.
// Use this for hooks that should be no-ops when Entire is not active.
func IsSetUpAndEnabled(ctx context.Context) bool {
	if !IsSetUp(ctx) {
		return false
	}
	s, err := Load(ctx)
	if err != nil {
		return false
	}
	return s.Enabled
}

// IsSummarizeEnabled checks if auto-summarize is enabled in settings.
// Returns false by default if settings cannot be loaded or the key is missing.
func IsSummarizeEnabled(ctx context.Context) bool {
	settings, err := Load(ctx)
	if err != nil {
		return false
	}
	return settings.IsSummarizeEnabled()
}

// IsSummarizeEnabled checks if auto-summarize is enabled in this settings instance.
func (s *EntireSettings) IsSummarizeEnabled() bool {
	if s.StrategyOptions == nil {
		return false
	}
	summarizeOpts, ok := s.StrategyOptions["summarize"].(map[string]any)
	if !ok {
		return false
	}
	enabled, ok := summarizeOpts["enabled"].(bool)
	if !ok {
		return false
	}
	return enabled
}

// IsPushSessionsDisabled checks if push_sessions is disabled in settings.
// Returns true if push_sessions is explicitly set to false.
func (s *EntireSettings) IsPushSessionsDisabled() bool {
	if s.StrategyOptions == nil {
		return false
	}
	val, exists := s.StrategyOptions["push_sessions"]
	if !exists {
		return false
	}
	if boolVal, ok := val.(bool); ok {
		return !boolVal // disabled = !push_sessions
	}
	return false
}

// FilesWithDeprecatedStrategy returns the relative paths of settings files
// that still contain the deprecated "strategy" field.
func FilesWithDeprecatedStrategy(ctx context.Context) []string {
	var files []string
	for _, rel := range []string{EntireSettingsFile, EntireSettingsLocalFile} {
		abs, err := paths.AbsPath(ctx, rel)
		if err != nil {
			abs = rel // Fallback to relative
		}
		s, err := LoadFromFile(abs)
		if err != nil || s.Strategy == "" {
			continue
		}
		files = append(files, rel)
	}
	return files
}

// WriteDeprecatedStrategyWarnings writes user-friendly deprecation warnings
// for each settings file that still contains the "strategy" field.
// Returns true if any warnings were written.
func WriteDeprecatedStrategyWarnings(ctx context.Context, w io.Writer) bool {
	files := FilesWithDeprecatedStrategy(ctx)
	for _, f := range files {
		fmt.Fprintf(w, "Note: \"%s\" in %s is no longer needed and can be removed. 'manual-commit' is now the only supported strategy.\n", "strategy", f)
	}
	return len(files) > 0
}

// Save saves the settings to .entire/settings.json.
func Save(ctx context.Context, settings *EntireSettings) error {
	return saveToFile(ctx, settings, EntireSettingsFile)
}

// SaveLocal saves the settings to .entire/settings.local.json.
func SaveLocal(ctx context.Context, settings *EntireSettings) error {
	return saveToFile(ctx, settings, EntireSettingsLocalFile)
}

// saveToFile saves settings to the specified file path.
func saveToFile(ctx context.Context, settings *EntireSettings, filePath string) error {
	// Get absolute path for the file
	filePathAbs, err := paths.AbsPath(ctx, filePath)
	if err != nil {
		filePathAbs = filePath // Fallback to relative
	}

	// Ensure directory exists
	dir := filepath.Dir(filePathAbs)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("creating settings directory: %w", err)
	}

	data, err := jsonutil.MarshalIndentWithNewline(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling settings: %w", err)
	}

	//nolint:gosec // G306: settings file is config, not secrets; 0o644 is appropriate
	if err := os.WriteFile(filePathAbs, data, 0o644); err != nil {
		return fmt.Errorf("writing settings file: %w", err)
	}
	return nil
}
