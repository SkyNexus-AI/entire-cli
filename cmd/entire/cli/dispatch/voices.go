package dispatch

import (
	_ "embed"
	"strings"
)

//go:embed voices/neutral.md
var voiceNeutral string

//go:embed voices/marvin.md
var voiceMarvin string

type Voice struct {
	Name     string
	Text     string
	IsPreset bool
}

var presets = map[string]string{
	"neutral": voiceNeutral,
	"marvin":  voiceMarvin,
}

func ResolveVoice(value string) Voice {
	if value == "" {
		return Voice{Name: "neutral", Text: voiceNeutral, IsPreset: true}
	}

	name := strings.ToLower(strings.TrimSpace(value))
	if text, ok := presets[name]; ok {
		return Voice{Name: name, Text: text, IsPreset: true}
	}

	return Voice{Text: value}
}

func ListPresetNames() []string {
	return []string{"neutral", "marvin"}
}
