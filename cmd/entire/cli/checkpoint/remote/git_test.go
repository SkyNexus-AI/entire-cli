package remote

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractRemoteFromArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"fetch with URL", []string{"fetch", "https://github.com/org/repo.git", "refs/heads/main"}, "https://github.com/org/repo.git"},
		{"push with flags", []string{"push", "--no-verify", "--porcelain", "origin", "main"}, "origin"},
		{"ls-remote", []string{"ls-remote", "origin", "refs/heads/*"}, "origin"},
		{"fetch with filter", []string{"fetch", "--no-tags", "--filter=blob:none", "https://host/r.git", "+refs/heads/main:refs/tmp"}, "https://host/r.git"},
		{"empty args", []string{}, ""},
		{"subcommand only", []string{"fetch"}, ""},
		{"only flags", []string{"fetch", "--no-tags"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, extractRemoteFromArgs(tt.args))
		})
	}
}

func TestResolveTargetProtocol(t *testing.T) {
	t.Parallel()

	t.Run("HTTPS URL", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, ProtocolHTTPS, resolveTargetProtocol(context.Background(), "https://github.com/org/repo.git"))
	})

	t.Run("SSH SCP URL", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, ProtocolSSH, resolveTargetProtocol(context.Background(), "git@github.com:org/repo.git"))
	})

	t.Run("SSH protocol URL", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, ProtocolSSH, resolveTargetProtocol(context.Background(), "ssh://git@github.com/org/repo.git"))
	})

	t.Run("local path returns empty", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, resolveTargetProtocol(context.Background(), "/tmp/some-bare-repo"))
	})

	t.Run("nonexistent remote name returns empty", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, resolveTargetProtocol(context.Background(), "nonexistent-remote"))
	})
}

// Not parallel: uses t.Chdir()
func TestResolveTargetProtocol_RemoteName(t *testing.T) {
	ctx := context.Background()

	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "f.txt", "init")
	testutil.GitAdd(t, tmpDir, "f.txt")
	testutil.GitCommit(t, tmpDir, "init")

	cmd := exec.CommandContext(ctx, "git", "remote", "add", "origin", "https://github.com/org/repo.git")
	cmd.Dir = tmpDir
	cmd.Env = testutil.GitIsolatedEnv()
	require.NoError(t, cmd.Run())

	t.Chdir(tmpDir)

	assert.Equal(t, ProtocolHTTPS, resolveTargetProtocol(ctx, "origin"))
}

// Not parallel: uses t.Chdir()
func TestResolveTargetProtocol_SSHRemoteName(t *testing.T) {
	ctx := context.Background()

	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "f.txt", "init")
	testutil.GitAdd(t, tmpDir, "f.txt")
	testutil.GitCommit(t, tmpDir, "init")

	cmd := exec.CommandContext(ctx, "git", "remote", "add", "origin", "git@github.com:org/repo.git")
	cmd.Dir = tmpDir
	cmd.Env = testutil.GitIsolatedEnv()
	require.NoError(t, cmd.Run())

	t.Chdir(tmpDir)

	assert.Equal(t, ProtocolSSH, resolveTargetProtocol(ctx, "origin"))
}

// Not parallel: uses t.Chdir()
func TestResolveFetchTarget(t *testing.T) {
	ctx := context.Background()

	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "f.txt", "init")
	testutil.GitAdd(t, tmpDir, "f.txt")
	testutil.GitCommit(t, tmpDir, "init")

	cmd := exec.CommandContext(ctx, "git", "remote", "add", "origin", "https://github.com/org/repo.git")
	cmd.Dir = tmpDir
	cmd.Env = testutil.GitIsolatedEnv()
	require.NoError(t, cmd.Run())

	t.Chdir(tmpDir)

	t.Run("disabled returns remote name", func(t *testing.T) {
		target, err := ResolveFetchTarget(ctx, "origin")
		require.NoError(t, err)
		assert.Equal(t, "origin", target)
	})

	t.Run("enabled resolves remote to URL", func(t *testing.T) {
		testutil.WriteFile(
			t,
			tmpDir,
			".entire/settings.json",
			`{"enabled": true, "strategy_options": {"filtered_fetches": true}}`,
		)

		target, err := ResolveFetchTarget(ctx, "origin")
		require.NoError(t, err)
		assert.Equal(t, "https://github.com/org/repo.git", target)
	})

	t.Run("URL target stays unchanged", func(t *testing.T) {
		target, err := ResolveFetchTarget(ctx, "https://github.com/org/repo.git")
		require.NoError(t, err)
		assert.Equal(t, "https://github.com/org/repo.git", target)
	})
}

func TestAppendCheckpointTokenEnv(t *testing.T) {
	t.Parallel()

	t.Run("adds token env vars", func(t *testing.T) {
		t.Parallel()
		env := appendCheckpointTokenEnv([]string{"PATH=/usr/bin", "HOME=/home/user"}, "my-secret-token")
		assert.Contains(t, env, "PATH=/usr/bin")
		assert.Contains(t, env, "HOME=/home/user")
		assert.Contains(t, env, "GIT_CONFIG_COUNT=1")
		assert.Contains(t, env, "GIT_CONFIG_KEY_0=http.extraHeader")
		wantAuth := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:my-secret-token"))
		assert.Contains(t, env, "GIT_CONFIG_VALUE_0="+wantAuth)
	})

	t.Run("filters existing GIT_CONFIG entries", func(t *testing.T) {
		t.Parallel()
		env := appendCheckpointTokenEnv([]string{
			"PATH=/usr/bin",
			"GIT_CONFIG_COUNT=2",
			"GIT_CONFIG_KEY_0=some.key",
			"GIT_CONFIG_VALUE_0=some-value",
			"GIT_CONFIG_KEY_1=other.key",
			"GIT_CONFIG_VALUE_1=other-value",
		}, "new-token")

		for _, e := range env {
			if e == "GIT_CONFIG_COUNT=2" {
				t.Error("old GIT_CONFIG_COUNT should have been filtered")
			}
			if strings.Contains(e, "some.key") || strings.Contains(e, "some-value") {
				t.Error("old GIT_CONFIG_KEY/VALUE should have been filtered")
			}
		}

		assert.Contains(t, env, "GIT_CONFIG_COUNT=1")
		assert.Contains(t, env, "GIT_CONFIG_KEY_0=http.extraHeader")
		wantAuth := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:new-token"))
		assert.Contains(t, env, "GIT_CONFIG_VALUE_0="+wantAuth)
	})
}

func TestIsValidToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		token string
		valid bool
	}{
		{"normal token", "ghp_abc123XYZ", true},
		{"with hyphen and underscore", "token-with_special.chars", true},
		{"contains CR", "token\rinjection", false},
		{"contains LF", "token\ninjection", false},
		{"contains CRLF", "token\r\ninjection", false},
		{"contains null byte", "token\x00injection", false},
		{"contains tab", "token\tvalue", false},
		{"contains DEL", "token\x7Fvalue", false},
		{"contains bell", "token\x07value", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.valid, isValidToken(tt.token))
		})
	}
}

// Not parallel: uses t.Setenv()
func TestNewCommand_ControlCharsInToken(t *testing.T) {
	t.Setenv(CheckpointTokenEnvVar, "token\r\nEvil: injected-header")

	cmd := newCommand(context.Background(), "fetch", "https://github.com/org/repo.git")
	assert.Nil(t, cmd.Env, "env should not be set when token contains control characters")
}

// Not parallel: uses t.Setenv()
func TestNewCommand_NoToken(t *testing.T) {
	t.Setenv(CheckpointTokenEnvVar, "")

	cmd := newCommand(context.Background(), "fetch", "https://github.com/org/repo.git")
	assert.Nil(t, cmd.Stdin, "stdin should be nil")
	assert.Nil(t, cmd.Env, "env should not be set when token is empty")
}

// Not parallel: uses t.Setenv()
func TestNewCommand_WhitespaceToken(t *testing.T) {
	t.Setenv(CheckpointTokenEnvVar, "   ")

	cmd := newCommand(context.Background(), "fetch", "https://github.com/org/repo.git")
	assert.Nil(t, cmd.Env, "env should not be set when token is only whitespace")
}

// Not parallel: uses t.Setenv()
func TestNewCommand_HTTPS_InjectsToken(t *testing.T) {
	t.Setenv(CheckpointTokenEnvVar, "ghp_test123")

	cmd := newCommand(context.Background(), "fetch", "https://github.com/org/repo.git")
	require.NotNil(t, cmd.Env, "env should be set for HTTPS with token")

	envMap := envToMap(cmd.Env)
	assert.Equal(t, "1", envMap["GIT_CONFIG_COUNT"])
	assert.Equal(t, "http.extraHeader", envMap["GIT_CONFIG_KEY_0"])
	wantAuth := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:ghp_test123"))
	assert.Equal(t, wantAuth, envMap["GIT_CONFIG_VALUE_0"])
}

// Not parallel: uses t.Setenv() and os.Stderr
func TestNewCommand_SSH_WarnsAndSkips(t *testing.T) {
	t.Setenv(CheckpointTokenEnvVar, "ghp_test123")

	sshTokenWarningOnce = sync.Once{}
	t.Cleanup(func() { sshTokenWarningOnce = sync.Once{} })

	oldStderr := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { os.Stderr = oldStderr })
	os.Stderr = w

	cmd := newCommand(context.Background(), "push", "git@github.com:org/repo.git", "main")

	w.Close()
	os.Stderr = oldStderr

	var buf [4096]byte
	n, _ := r.Read(buf[:]) //nolint:errcheck // test helper, EOF is expected
	stderr := string(buf[:n])
	r.Close()

	assert.Nil(t, cmd.Env, "env should NOT be set for SSH targets")
	assert.Contains(t, stderr, "ENTIRE_CHECKPOINT_TOKEN")
	assert.Contains(t, stderr, "SSH")
	assert.Contains(t, stderr, "ignored")
}

// Not parallel: uses t.Setenv()
func TestNewCommand_LocalPath_NoToken(t *testing.T) {
	t.Setenv(CheckpointTokenEnvVar, "ghp_test123")

	cmd := newCommand(context.Background(), "push", "/tmp/bare-repo", "main")
	assert.Nil(t, cmd.Env, "env should NOT be set for local path targets")
}

// newTLSTestServer creates an HTTPS test server that captures the Authorization header.
// Returns the server and a function to read the captured auth header and request count.
func newTLSTestServer(t *testing.T) (*httptest.Server, func() (auth string, count int)) {
	t.Helper()

	var (
		mu           sync.Mutex
		capturedAuth string
		requestCount int
	)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		capturedAuth = r.Header.Get("Authorization")
		requestCount++
		mu.Unlock()

		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintln(w, "forbidden")
	}))
	t.Cleanup(srv.Close)

	return srv, func() (string, int) {
		mu.Lock()
		defer mu.Unlock()
		return capturedAuth, requestCount
	}
}

func setupTokenTestRepo(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	testutil.InitRepo(t, tmpDir)
	testutil.WriteFile(t, tmpDir, "f.txt", "init")
	testutil.GitAdd(t, tmpDir, "f.txt")
	testutil.GitCommit(t, tmpDir, "init")
	t.Chdir(tmpDir)
	return tmpDir
}

// Not parallel: uses t.Chdir()
func TestCheckpointToken_HTTPSServer_SendsAuthHeader(t *testing.T) {
	t.Setenv(CheckpointTokenEnvVar, "test-token-abc123")

	srv, getCapture := newTLSTestServer(t)
	tmpDir := setupTokenTestRepo(t)

	target := srv.URL + "/org/repo.git"
	cmd := newCommand(context.Background(),
		"fetch", target, "+refs/heads/main:refs/remotes/origin/main")
	cmd.Dir = tmpDir
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GIT_SSL_NO_VERIFY=1")
	_ = cmd.Run() //nolint:errcheck // expected to fail against test server

	auth, count := getCapture()
	require.Positive(t, count, "server should have received at least one request")
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:test-token-abc123"))
	assert.Equal(t, wantAuth, auth,
		"git should send the token as a Basic Authorization header")
}

// Not parallel: uses t.Chdir()
func TestCheckpointToken_HTTPSServer_NoTokenNoHeader(t *testing.T) {
	t.Setenv(CheckpointTokenEnvVar, "")

	srv, getCapture := newTLSTestServer(t)
	tmpDir := setupTokenTestRepo(t)

	target := srv.URL + "/org/repo.git"
	cmd := newCommand(context.Background(),
		"fetch", target, "+refs/heads/main:refs/remotes/origin/main")
	cmd.Dir = tmpDir
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GIT_SSL_NO_VERIFY=1")

	_ = cmd.Run() //nolint:errcheck // expected to fail against test server

	auth, count := getCapture()
	require.Positive(t, count, "server should have received at least one request")
	assert.Empty(t, auth, "no Authorization header should be sent without token")
}

// Not parallel: uses t.Chdir()
func TestCheckpointToken_HTTPSServer_LsRemoteSendsAuthHeader(t *testing.T) {
	t.Setenv(CheckpointTokenEnvVar, "push-token-xyz789")

	srv, getCapture := newTLSTestServer(t)
	tmpDir := setupTokenTestRepo(t)

	target := srv.URL + "/org/repo.git"
	cmd := newCommand(context.Background(),
		"ls-remote", target)
	cmd.Dir = tmpDir
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GIT_SSL_NO_VERIFY=1")

	_ = cmd.Run() //nolint:errcheck // expected to fail against test server

	auth, count := getCapture()
	require.Positive(t, count, "server should have received at least one request")
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:push-token-xyz789"))
	assert.Equal(t, wantAuth, auth,
		"git ls-remote should send the token as a Basic Authorization header")
}

// Not parallel: uses t.Setenv()
func TestNewCommand_GIT_TERMINAL_PROMPT_Coexistence(t *testing.T) {
	t.Setenv(CheckpointTokenEnvVar, "coexist-token")

	cmd := newCommand(context.Background(),
		"fetch", "--no-tags", "--filter=blob:none", "https://github.com/org/repo.git", "refs/heads/main")
	require.NotNil(t, cmd.Env)

	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0")

	envMap := envToMap(cmd.Env)
	assert.Equal(t, "1", envMap["GIT_CONFIG_COUNT"])
	wantAuth := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:coexist-token"))
	assert.Equal(t, wantAuth, envMap["GIT_CONFIG_VALUE_0"])
	assert.Equal(t, "0", envMap["GIT_TERMINAL_PROMPT"])
}

func TestIsURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		val  string
		want bool
	}{
		{"remote name", "origin", false},
		{"SSH SCP", "git@github.com:org/repo.git", true},
		{"HTTPS", "https://github.com/org/repo.git", true},
		{"SSH protocol", "ssh://git@github.com/org/repo.git", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, IsURL(tt.val))
		})
	}
}

// envToMap converts an env slice to a map for easy assertions.
// For duplicate keys, the last value wins.
func envToMap(env []string) map[string]string {
	m := make(map[string]string, len(env))
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if ok {
			m[k] = v
		}
	}
	return m
}
