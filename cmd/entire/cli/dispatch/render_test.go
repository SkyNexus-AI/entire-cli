package dispatch

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestRenderMarkdown_Golden(t *testing.T) {
	t.Parallel()

	got := RenderMarkdown(testDispatchFixture())
	want, err := os.ReadFile("testdata/markdown.golden")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(got) != strings.TrimSpace(string(want)) {
		t.Fatalf("mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func testDispatchFixture() *Dispatch {
	ts := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	return &Dispatch{
		Window: Window{
			NormalizedSince:   time.Date(2026, 4, 9, 0, 0, 0, 0, time.UTC),
			NormalizedUntil:   time.Date(2026, 4, 16, 0, 0, 0, 0, time.UTC),
			FirstCheckpointAt: ts,
			LastCheckpointAt:  ts,
		},
		CoveredRepos: []string{"entireio/cli"},
		Repos: []RepoGroup{{
			FullName: "entireio/cli",
			Sections: []Section{{
				Label: "CI & Tooling",
				Bullets: []Bullet{{
					CheckpointID: "a1b2c3d4e5f6",
					Text:         "Fixed hanging CI tests locally.",
					Source:       "cloud_analysis",
					Branch:       "main",
					CreatedAt:    ts,
				}},
			}},
		}},
		Totals: Totals{
			Checkpoints:         1,
			UsedCheckpointCount: 1,
			Branches:            1,
			FilesTouched:        2,
		},
		Warnings: Warnings{
			UnknownCount: 1,
		},
	}
}
