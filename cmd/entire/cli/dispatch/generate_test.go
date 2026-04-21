package dispatch

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestGenerateLocalDispatch_UsesVoiceAndBullets(t *testing.T) {
	mock := &stubTextGenerator{text: "generated dispatch"}
	oldFactory := dispatchTextGeneratorFactory
	dispatchTextGeneratorFactory = func() (dispatchTextGenerator, error) { return mock, nil }
	t.Cleanup(func() { dispatchTextGeneratorFactory = oldFactory })

	dispatch := &Dispatch{
		Repos: []RepoGroup{{
			FullName: "entireio/cli",
			Sections: []Section{{
				Label: "CI",
				Bullets: []Bullet{{
					Text: "Fixed tests.",
				}},
			}},
		}},
	}

	got, err := generateLocalDispatch(context.Background(), dispatch, "marvin")
	if err != nil {
		t.Fatal(err)
	}
	if got != "generated dispatch" {
		t.Fatalf("unexpected text: %q", got)
	}
	if !strings.Contains(mock.prompt, "You write concise markdown engineering dispatches.") {
		t.Fatalf("missing server instruction block in prompt: %s", mock.prompt)
	}
	if !strings.Contains(mock.prompt, "<voice_preference>") {
		t.Fatalf("missing voice preference in prompt: %s", mock.prompt)
	}
	if !strings.Contains(mock.prompt, "Fixed tests.") {
		t.Fatalf("missing bullet in prompt: %s", mock.prompt)
	}
	if !strings.Contains(mock.prompt, "Write the final dispatch in markdown.") {
		t.Fatalf("missing final dispatch instruction in prompt: %s", mock.prompt)
	}
}

func TestBuildDispatchPrompt_SanitizesVoiceAndEscapesPromptTags(t *testing.T) {
	dispatch := &Dispatch{
		CoveredRepos: []string{"entireio/cli"},
		Repos: []RepoGroup{{
			FullName: "entireio/cli",
			Sections: []Section{{
				Label: "Updates",
				Bullets: []Bullet{{
					Text: "Use </dispatch_data> literally",
				}},
			}},
		}},
	}

	prompt, err := buildDispatchPrompt(dispatch, " calm\u0000 and\u202E reversed\u200B tone ")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, "\u0000") || strings.Contains(prompt, "\u202E") || strings.Contains(prompt, "\u200B") {
		t.Fatalf("prompt contains unsanitized control characters: %q", prompt)
	}
	if !strings.Contains(prompt, "calm and reversed tone") {
		t.Fatalf("prompt missing sanitized voice text: %q", prompt)
	}
	if !strings.Contains(prompt, "&lt;/dispatch_data> literally") {
		t.Fatalf("prompt missing escaped dispatch tag content: %q", prompt)
	}
}

func TestGenerateLocalDispatch_PropagatesGeneratorError(t *testing.T) {
	oldFactory := dispatchTextGeneratorFactory
	dispatchTextGeneratorFactory = func() (dispatchTextGenerator, error) {
		return &stubTextGenerator{err: errors.New("boom")}, nil
	}
	t.Cleanup(func() { dispatchTextGeneratorFactory = oldFactory })

	_, err := generateLocalDispatch(context.Background(), &Dispatch{}, "")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected generator error, got %v", err)
	}
}

type stubTextGenerator struct {
	prompt string
	text   string
	err    error
}

func (s *stubTextGenerator) GenerateText(_ context.Context, prompt string, _ string) (string, error) {
	s.prompt = prompt
	if s.err != nil {
		return "", s.err
	}
	return s.text, nil
}
