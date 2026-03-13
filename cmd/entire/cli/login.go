package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/auth"
	"github.com/spf13/cobra"
)

const fallbackDeviceAuthPollInterval = time.Second

var browserOpener = openBrowser

func newLoginCmd() *cobra.Command {
	var printBrowserURL bool

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to Entire",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runLogin(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), printBrowserURL)
		},
	}

	cmd.Flags().BoolVar(&printBrowserURL, "print-browser-url", false, "Print the approval URL instead of opening a browser")

	return cmd
}

func runLogin(ctx context.Context, outW, errW io.Writer, printBrowserURL bool) error {
	client := auth.NewClient(nil)

	start, err := client.StartDeviceAuth(ctx)
	if err != nil {
		return fmt.Errorf("start login: %w", err)
	}

	fmt.Fprintf(outW, "Device code: %s\n", start.UserCode)
	approvalURL := start.VerificationURIComplete
	if approvalURL == "" {
		approvalURL = start.VerificationURI
	}

	fmt.Fprintf(outW, "Approval URL: %s\n", approvalURL)

	if printBrowserURL {
		fmt.Fprintln(outW, "Open the approval URL in your browser to continue.")
	} else {
		if err := browserOpener(ctx, approvalURL); err != nil {
			fmt.Fprintf(errW, "Warning: failed to open browser automatically: %v\n", err)
			fmt.Fprintln(outW, "Open the approval URL in your browser to continue.")
		} else {
			fmt.Fprintln(outW, "Opened your browser for approval.")
		}
	}

	fmt.Fprintln(outW, "Waiting for approval...")

	token, err := waitForApproval(ctx, client, start.DeviceCode, start.ExpiresIn, start.Interval)
	if err != nil {
		return fmt.Errorf("complete login: %w", err)
	}

	store, err := auth.NewStore()
	if err != nil {
		return fmt.Errorf("create auth store: %w", err)
	}

	if err := store.SaveToken(client.BaseURL(), token); err != nil {
		return fmt.Errorf("save auth token: %w", err)
	}

	fmt.Fprintf(outW, "Login complete. Saved token to %s\n", store.FilePath())
	return nil
}

func waitForApproval(ctx context.Context, client *auth.Client, deviceCode string, expiresIn, interval int) (string, error) {
	deadline := time.Now().Add(time.Duration(expiresIn) * time.Second)
	pollInterval := time.Duration(interval) * time.Second
	if pollInterval <= 0 {
		pollInterval = fallbackDeviceAuthPollInterval
	}

	for {
		if time.Now().After(deadline) {
			return "", errors.New("device authorization expired")
		}

		result, err := client.PollDeviceAuth(ctx, deviceCode)
		if err != nil {
			return "", fmt.Errorf("poll approval status: %w", err)
		}

		switch result.Error {
		case "", "authorization_pending":
			// continue below
		case "slow_down":
			pollInterval += time.Second
		case "access_denied":
			return "", errors.New("device authorization denied")
		case "expired_token":
			return "", errors.New("device authorization expired")
		default:
			return "", fmt.Errorf("device authorization failed: %s", result.Error)
		}

		if result.Error == "" {
			if result.AccessToken == "" {
				return "", errors.New("device authorization completed without a token")
			}
			return result.AccessToken, nil
		}

		select {
		case <-ctx.Done():
			return "", fmt.Errorf("wait for approval: %w", ctx.Err())
		case <-time.After(pollInterval):
		}
	}
}

func openBrowser(ctx context.Context, browserURL string) error {
	var command string
	var args []string

	switch runtime.GOOS {
	case "darwin":
		command = "open"
		args = []string{browserURL}
	case "linux":
		command = "xdg-open"
		args = []string{browserURL}
	default:
		return fmt.Errorf("unsupported platform %s", runtime.GOOS)
	}

	cmd := exec.CommandContext(ctx, command, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start browser command %q: %w", command, err)
	}

	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("release browser process: %w", err)
	}

	return nil
}
