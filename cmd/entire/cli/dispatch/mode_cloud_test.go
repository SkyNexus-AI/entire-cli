package dispatch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

func TestServerMode_HappyPath(t *testing.T) {
	dir := t.TempDir()
	testutil.InitRepo(t, dir)
	testutil.WriteFile(t, dir, "a.txt", "x")
	testutil.GitAdd(t, dir, "a.txt")
	testutil.GitCommit(t, dir, "initial")
	addOriginRemote(t, dir, "https://github.com/entireio/cli.git")

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/dispatches" {
			http.NotFound(w, r)
			return
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		repo, ok := body["repo"].(string)
		if !ok || repo != "entireio/cli" {
			t.Fatalf("unexpected repo payload: %v", body)
		}
		if _, ok := body["repos"]; ok {
			t.Fatalf("did not expect repos payload: %v", body)
		}
		if body["until"] != "2026-04-15T18:30:00Z" {
			t.Fatalf("unexpected until payload: %v", body["until"])
		}
		if body["generate"] != true {
			t.Fatalf("expected generate=true payload, got %v", body["generate"])
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(map[string]any{
			"window": map[string]any{
				"normalized_since": "2026-04-09T00:00:00Z",
				"normalized_until": "2026-04-16T00:00:00Z",
			},
			"covered_repos": []string{"entireio/cli"},
			"repos":         []any{},
			"totals": map[string]any{
				"checkpoints":           0,
				"used_checkpoint_count": 0,
				"branches":              0,
				"files_touched":         0,
			},
			"warnings": map[string]any{
				"access_denied_count": 0,
				"pending_count":       0,
				"failed_count":        0,
				"unknown_count":       0,
				"uncategorized_count": 0,
			},
			"generated_markdown": "Hello",
		}); err != nil {
			t.Fatal(err)
		}
	}))
	defer mock.Close()

	oldLookup := lookupCurrentToken
	oldNow := nowUTC
	lookupCurrentToken = func() (string, error) { return "test-token", nil }
	nowUTC = func() time.Time { return time.Date(2026, 4, 16, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() {
		lookupCurrentToken = oldLookup
		nowUTC = oldNow
	})

	t.Setenv("ENTIRE_API_BASE_URL", mock.URL)
	t.Chdir(dir)

	got, err := Run(context.Background(), Options{
		Mode:     ModeServer,
		Since:    "7d",
		Until:    "2026-04-15T18:30:00Z",
		Branches: []string{"main"},
		Voice:    "neutral",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.GeneratedText != "Hello" {
		t.Fatalf("bad text: %q", got.GeneratedText)
	}
}

func TestServerMode_ExplicitReposDoNotRequireCurrentRepo(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/dispatches" {
			http.NotFound(w, r)
			return
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		repos, ok := body["repo"].([]any)
		if !ok || len(repos) != 2 || repos[0] != "entireio/cli" || repos[1] != "entireio/entire.io" {
			t.Fatalf("unexpected repo payload: %v", body)
		}
		if _, ok := body["repos"]; ok {
			t.Fatalf("did not expect repos payload: %v", body)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(map[string]any{
			"window": map[string]any{
				"normalized_since": "2026-04-09T00:00:00Z",
				"normalized_until": "2026-04-16T00:00:00Z",
			},
			"covered_repos":      []string{"entireio/cli", "entireio/entire.io"},
			"repos":              []any{},
			"generated_markdown": "Hello",
			"totals": map[string]any{
				"checkpoints":           0,
				"used_checkpoint_count": 0,
				"branches":              0,
				"files_touched":         0,
			},
			"warnings": map[string]any{
				"access_denied_count": 0,
				"pending_count":       0,
				"failed_count":        0,
				"unknown_count":       0,
				"uncategorized_count": 0,
			},
		}); err != nil {
			t.Fatal(err)
		}
	}))
	defer mock.Close()

	oldLookup := lookupCurrentToken
	oldNow := nowUTC
	lookupCurrentToken = func() (string, error) { return "test-token", nil }
	nowUTC = func() time.Time { return time.Date(2026, 4, 16, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() {
		lookupCurrentToken = oldLookup
		nowUTC = oldNow
	})

	t.Setenv("ENTIRE_API_BASE_URL", mock.URL)

	got, err := Run(context.Background(), Options{
		Mode:      ModeServer,
		RepoPaths: []string{"entireio/cli", "entireio/entire.io"},
		Since:     "7d",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected dispatch result")
	}
	if len(got.CoveredRepos) != 2 {
		t.Fatalf("expected covered repos to propagate, got %v", got.CoveredRepos)
	}
}

func TestServerMode_RequiresGeneratedMarkdown(t *testing.T) {
	dir := t.TempDir()
	testutil.InitRepo(t, dir)
	testutil.WriteFile(t, dir, "a.txt", "x")
	testutil.GitAdd(t, dir, "a.txt")
	testutil.GitCommit(t, dir, "initial")
	addOriginRemote(t, dir, "https://github.com/entireio/cli.git")

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/dispatches" {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(map[string]any{
			"window": map[string]any{
				"normalized_since": "2026-04-09T00:00:00Z",
				"normalized_until": "2026-04-16T00:00:00Z",
			},
			"covered_repos": []string{"entireio/cli"},
			"repos":         []any{},
			"totals": map[string]any{
				"checkpoints":           0,
				"used_checkpoint_count": 0,
				"branches":              0,
				"files_touched":         0,
			},
			"warnings": map[string]any{
				"access_denied_count": 0,
				"pending_count":       0,
				"failed_count":        0,
				"unknown_count":       0,
				"uncategorized_count": 0,
			},
		}); err != nil {
			t.Fatal(err)
		}
	}))
	defer mock.Close()

	oldLookup := lookupCurrentToken
	oldNow := nowUTC
	lookupCurrentToken = func() (string, error) { return "test-token", nil }
	nowUTC = func() time.Time { return time.Date(2026, 4, 16, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() {
		lookupCurrentToken = oldLookup
		nowUTC = oldNow
	})

	t.Setenv("ENTIRE_API_BASE_URL", mock.URL)
	t.Chdir(dir)

	_, err := Run(context.Background(), Options{
		Mode:  ModeServer,
		Since: "7d",
	})
	if err == nil {
		t.Fatal("expected error when server response omits generated markdown")
	}
	if err.Error() != "dispatch generation returned no markdown" {
		t.Fatalf("unexpected error: %v", err)
	}
}
