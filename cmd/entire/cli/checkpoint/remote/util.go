package remote

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/settings"
)

const (
	originRemote          = "origin"
	checkpointTokenEnvVar = "ENTIRE_CHECKPOINT_TOKEN"
)

const (
	ProtocolSSH   = "ssh"
	ProtocolHTTPS = "https"
)

type RemoteInfo struct {
	Protocol string
	Host     string
	Owner    string
	Repo     string
}

// FetchURL returns the effective checkpoint fetch URL for the current repository.
// If strategy_options.checkpoint_remote is configured, the returned URL is derived
// from the origin remote's protocol/host and the configured checkpoint repo.
// Otherwise, the origin remote URL is returned directly.
//
// If ENTIRE_CHECKPOINT_TOKEN is set and a checkpoint remote is configured, HTTPS is
// forced so the token can be used even when origin is configured via SSH.
func FetchURL(ctx context.Context) (string, error) {
	withToken := strings.TrimSpace(os.Getenv(checkpointTokenEnvVar)) != ""

	originURL, originErr := GetRemoteURL(ctx, originRemote)
	if originErr != nil {
		originURL = ""
	}

	if originURL != "" && withToken {
		if tokenURL, ok := deriveTokenOriginURL(originURL); ok {
			originURL = tokenURL
		}
	}

	s, err := settings.Load(ctx)
	if err != nil {
		if originURL != "" {
			logFallback(ctx, "fetch", originURL, "load settings", err)
			return originURL, nil
		}
		return "", fmt.Errorf("load settings: %w", err)
	}

	config := s.GetCheckpointRemote()
	if config == nil {
		if originURL == "" {
			return "", fmt.Errorf("no fetch URL found: %w", originErr)
		}
		return originURL, nil
	}

	if withToken {
		host, ok := providerHost(config.Provider)
		if ok {
			checkpointURL, err := deriveCheckpointURLFromInfo(&RemoteInfo{
				Protocol: ProtocolHTTPS,
				Host:     host,
			}, config)
			if err == nil {
				return checkpointURL, nil
			}
		}

		// In token-based execution path, short-circuit to avoid additional
		// change in protocol.
		if originURL != "" {
			return originURL, nil
		}
	}

	if originURL == "" {
		return "", fmt.Errorf("no fetch URL found: %w", originErr)
	}

	info, err := ParseURL(originURL)
	if err != nil {
		logFallback(ctx, "fetch", originURL, "parse origin remote URL", err)
		return originURL, nil
	}

	checkpointURL, err := deriveCheckpointURLFromInfo(info, config)
	if err != nil {
		logFallback(ctx, "fetch", originURL, "derive checkpoint remote URL", err)
		return originURL, nil
	}

	return checkpointURL, nil
}

// PushURL returns the effective checkpoint push URL for the current repository.
// Unlike FetchURL:
//   - it derives protocol from the requested push remote, not always origin
//   - it skips checkpoint remote use when the push remote owner differs
//     from the configured checkpoint remote owner
//
// If ENTIRE_CHECKPOINT_TOKEN is set, HTTPS is forced so the token can be used
// even when the push remote is configured via SSH.
//
// The boolean return value reports whether a dedicated checkpoint_remote is
// configured and should be used for push. When false, the returned URL is the
// repository's origin URL as a fallback.
func PushURL(ctx context.Context, pushRemoteName string) (string, bool, error) {
	originURL, _ := GetRemoteURL(ctx, originRemote)

	s, err := settings.Load(ctx)
	if err != nil {
		fallbackURL, fallbackErr := resolvePushFallbackURL(ctx, pushRemoteName, originURL)
		if fallbackErr == nil {
			logFallback(ctx, "push", fallbackURL, "load settings", err,
				slog.String("push_remote", pushRemoteName),
			)
			return fallbackURL, false, nil
		}
		return "", false, fmt.Errorf("load settings: %w", err)
	}

	config := s.GetCheckpointRemote()
	if config == nil {
		fallbackURL, fallbackErr := resolvePushFallbackURL(ctx, pushRemoteName, originURL)
		if fallbackErr != nil {
			return "", false, fmt.Errorf("no push URL found: %w", fallbackErr)
		}
		return fallbackURL, false, nil
	}

	pushRemoteURL, err := GetRemoteURL(ctx, pushRemoteName)
	if err != nil {
		fallbackURL, fallbackErr := resolvePushFallbackURL(ctx, pushRemoteName, originURL)
		if fallbackErr == nil {
			logFallback(ctx, "push", fallbackURL, "get push remote URL", err,
				slog.String("push_remote", pushRemoteName),
			)
			return fallbackURL, false, nil
		}
		return "", true, fmt.Errorf("no push URL found: %w", err)
	}

	pushInfo, err := ParseURL(pushRemoteURL)
	if err != nil {
		if originURL != "" {
			logFallback(ctx, "push", originURL, "parse push remote URL", err,
				slog.String("push_remote", pushRemoteName),
			)
			return originURL, false, nil
		}
		return "", true, fmt.Errorf("no push URL found: %w", err)
	}
	if strings.TrimSpace(os.Getenv(checkpointTokenEnvVar)) != "" {
		pushInfo = &RemoteInfo{
			Protocol: ProtocolHTTPS,
			Host:     pushInfo.Host,
			Owner:    pushInfo.Owner,
			Repo:     pushInfo.Repo,
		}
	}

	checkpointOwner := config.Owner()
	if pushInfo.Owner != "" && checkpointOwner != "" && !strings.EqualFold(pushInfo.Owner, checkpointOwner) {
		fallbackURL, fallbackErr := resolvePushFallbackURL(ctx, pushRemoteName, originURL)
		if fallbackErr != nil {
			return "", false, fmt.Errorf("no push URL found: %w", fallbackErr)
		}
		return fallbackURL, false, nil
	}

	pushURL, err := deriveCheckpointURLFromInfo(pushInfo, config)
	if err != nil {
		fallbackURL, fallbackErr := resolvePushFallbackURL(ctx, pushRemoteName, originURL)
		if fallbackErr == nil {
			logFallback(ctx, "push", fallbackURL, "derive push checkpoint URL", err,
				slog.String("push_remote", pushRemoteName),
			)
			return fallbackURL, false, nil
		}
		return "", true, fmt.Errorf("no push URL found: %w", err)
	}

	return pushURL, true, nil
}

// Configured reports whether a structured checkpoint_remote is configured.
func Configured(ctx context.Context) (bool, error) {
	s, err := settings.Load(ctx)
	if err != nil {
		logging.Warn(ctx, "checkpoint remote configuration unavailable; treating as not configured",
			slog.String("error", err.Error()),
		)
		return false, nil
	}
	return s.GetCheckpointRemote() != nil, nil
}

func GetRemoteURL(ctx context.Context, remoteName string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", remoteName)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("remote %q not found", remoteName)
	}
	return strings.TrimSpace(string(output)), nil
}

func ParseURL(rawURL string) (*RemoteInfo, error) {
	rawURL = strings.TrimSpace(rawURL)

	if strings.Contains(rawURL, ":") && !strings.Contains(rawURL, "://") {
		parts := strings.SplitN(rawURL, ":", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid SSH URL: %s", RedactURL(rawURL))
		}

		hostPart := parts[0]
		host := hostPart
		if idx := strings.Index(hostPart, "@"); idx >= 0 {
			host = hostPart[idx+1:]
		}

		pathPart := strings.TrimSuffix(parts[1], ".git")
		owner, repo, err := splitOwnerRepo(pathPart)
		if err != nil {
			return nil, err
		}

		return &RemoteInfo{Protocol: ProtocolSSH, Host: host, Owner: owner, Repo: repo}, nil
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %s", RedactURL(rawURL))
	}
	if u.Scheme == "" {
		return nil, fmt.Errorf("no protocol in URL: %s", RedactURL(rawURL))
	}

	pathPart := strings.TrimPrefix(u.Path, "/")
	owner, repo, err := splitOwnerRepo(pathPart)
	if err != nil {
		return nil, err
	}

	return &RemoteInfo{Protocol: u.Scheme, Host: u.Hostname(), Owner: owner, Repo: repo}, nil
}

func DeriveCheckpointURL(pushRemoteURL string, config *settings.CheckpointRemoteConfig) (string, error) {
	info, err := ParseURL(pushRemoteURL)
	if err != nil {
		return "", fmt.Errorf("cannot parse push remote URL: %w", err)
	}
	return deriveCheckpointURLFromInfo(info, config)
}

func ExtractOwnerFromRemoteURL(rawURL string) string {
	info, err := ParseURL(rawURL)
	if err != nil {
		return ""
	}
	return info.Owner
}

func deriveCheckpointURLFromInfo(info *RemoteInfo, config *settings.CheckpointRemoteConfig) (string, error) {
	switch info.Protocol {
	case ProtocolSSH:
		return fmt.Sprintf("git@%s:%s.git", info.Host, config.Repo), nil
	case ProtocolHTTPS:
		return fmt.Sprintf("https://%s/%s.git", info.Host, config.Repo), nil
	default:
		return "", fmt.Errorf("unsupported protocol %q in origin remote", info.Protocol)
	}
}

func deriveTokenOriginURL(originURL string) (string, bool) {
	info, err := ParseURL(originURL)
	if err != nil {
		return "", false
	}
	if info.Host == "" || info.Owner == "" || info.Repo == "" {
		return "", false
	}
	return fmt.Sprintf("https://%s/%s/%s.git", info.Host, info.Owner, info.Repo), true
}

func providerHost(provider string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "github":
		return "github.com", true
	case "gitlab":
		return "gitlab.com", true
	default:
		return "", false
	}
}

func splitOwnerRepo(path string) (string, string, error) {
	path = strings.TrimSuffix(path, ".git")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("cannot parse owner/repo from path: %s", path)
	}
	return parts[0], parts[1], nil
}

func RedactURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		if idx := strings.Index(rawURL, "@"); idx >= 0 {
			if colonIdx := strings.Index(rawURL[idx:], ":"); colonIdx >= 0 {
				return rawURL[idx+1:idx+colonIdx] + ":***"
			}
		}
		return "<unparseable>"
	}
	u.User = nil
	u.RawQuery = ""
	return u.Scheme + "://" + u.Host + u.Path
}

func logFallback(ctx context.Context, operation, fallbackURL, reason string, err error, attrs ...any) {
	logAttrs := []any{
		slog.String("operation", operation),
		slog.String("fallback_url", RedactURL(fallbackURL)),
		slog.String("reason", reason),
		slog.String("error", err.Error()),
	}
	logAttrs = append(logAttrs, attrs...)
	logging.Warn(ctx, "checkpoint remote URL resolution fell back to alternate remote URL", logAttrs...)
}

func resolvePushFallbackURL(ctx context.Context, pushRemoteName, originURL string) (string, error) {
	if originURL != "" {
		return originURL, nil
	}
	if pushRemoteName == "" || pushRemoteName == originRemote {
		return "", fmt.Errorf("remote %q not found", originRemote)
	}
	return GetRemoteURL(ctx, pushRemoteName)
}
