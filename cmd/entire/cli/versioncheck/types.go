package versioncheck

import (
	"os"
	"time"
)

// VersionCache represents the cached version check data.
type VersionCache struct {
	LastCheckTime time.Time `json:"last_check_time"`
}

// GitHubRelease represents the GitHub API response for a release.
type GitHubRelease struct {
	TagName    string `json:"tag_name"`
	Prerelease bool   `json:"prerelease"`
}

// githubAPIURL is the GitHub API endpoint for fetching the latest stable release.
// A var so tests can override it directly. For local manual testing, set the
// ENTIRE_VERSION_CHECK_URL env var to point at a mock server — this avoids
// hammering api.github.com and working around rate limits while iterating.
var githubAPIURL = envOr("ENTIRE_VERSION_CHECK_URL",
	"https://api.github.com/repos/entireio/cli/releases/latest")

// githubReleasesURL is the GitHub API endpoint for listing releases (used for nightly checks).
// Overridable via ENTIRE_VERSION_CHECK_RELEASES_URL for the same reason.
var githubReleasesURL = envOr("ENTIRE_VERSION_CHECK_RELEASES_URL",
	"https://api.github.com/repos/entireio/cli/releases")

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

const (
	// checkInterval is the duration between version checks.
	checkInterval = 24 * time.Hour

	// httpTimeout is the timeout for HTTP requests to the GitHub API.
	httpTimeout = 2 * time.Second

	// cacheFileName is the name of the cache file stored in the global config directory.
	cacheFileName = "version_check.json"

	// globalConfigDirName is the name of the global config directory in the user's home.
	globalConfigDirName = ".config/entire"
)
