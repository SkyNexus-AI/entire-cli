package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/entireio/cli/cmd/entire/cli/api"
	"github.com/entireio/cli/cmd/entire/cli/auth"
	"github.com/spf13/cobra"
)

// authTokenLister lists API tokens for the authenticated user.
type authTokenLister func(ctx context.Context, token string) ([]api.Token, error)

// authTokenRevoker revokes a single API token by id.
type authTokenRevoker func(ctx context.Context, callerToken, id string) error

// User-visible placeholder strings. Promoted to constants so tests and
// production share a single source of truth.
const (
	placeholderDash = "-"
	lastUsedNever   = "never"
	lastUsedJustNow = "just now"
)

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage authentication and API tokens",
		Long:  "Authentication subcommands. Includes login, logout, status, listing tokens, and revoking tokens.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newLoginCmd())
	cmd.AddCommand(newLogoutCmd())
	cmd.AddCommand(newAuthStatusCmd())
	cmd.AddCommand(newAuthListCmd())
	cmd.AddCommand(newAuthRevokeCmd())
	return cmd
}

// --- status -----------------------------------------------------------------

func newAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show authentication status",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuthStatus(cmd.Context(), cmd.OutOrStdout(),
				auth.NewStore(), defaultListTokens, api.BaseURL())
		},
	}
}

func defaultListTokens(ctx context.Context, token string) ([]api.Token, error) {
	return api.NewClient(token).ListTokens(ctx) //nolint:wrapcheck // ListTokens already wraps with action context
}

func runAuthStatus(ctx context.Context, w io.Writer, store logoutTokenStore, list authTokenLister, baseURL string) error {
	token, err := store.GetToken(baseURL)
	if err != nil {
		return fmt.Errorf("read keychain: %w", err)
	}
	if token == "" {
		fmt.Fprintf(w, "Not logged in to %s\n", baseURL)
		fmt.Fprintln(w, "Run 'entire login' to authenticate.")
		return nil
	}

	tokens, err := list(ctx, token)
	if err != nil {
		if api.IsHTTPErrorStatus(err, http.StatusUnauthorized) {
			fmt.Fprintf(w, "Token in keychain for %s is no longer valid.\n", baseURL)
			fmt.Fprintln(w, "Run 'entire login' to re-authenticate.")
			return nil
		}
		return fmt.Errorf("validate token: %w", err)
	}

	fmt.Fprintf(w, "Logged in to %s\n", baseURL)
	fmt.Fprintln(w, "  Token: stored in OS keychain")
	fmt.Fprintf(w, "  Active tokens on this account: %d\n", len(tokens))
	return nil
}

// --- list -------------------------------------------------------------------

func newAuthListCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List active API tokens for the authenticated user",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuthList(cmd.Context(), cmd.OutOrStdout(),
				auth.NewStore(), defaultListTokens, api.BaseURL(), jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Print tokens as JSON")
	return cmd
}

func runAuthList(ctx context.Context, w io.Writer, store logoutTokenStore, list authTokenLister, baseURL string, jsonOut bool) error {
	token, err := store.GetToken(baseURL)
	if err != nil {
		return fmt.Errorf("read keychain: %w", err)
	}
	if token == "" {
		return fmt.Errorf("not logged in to %s; run 'entire login' first", baseURL)
	}

	tokens, err := list(ctx, token)
	if err != nil {
		return err
	}

	if jsonOut {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		if err := enc.Encode(tokens); err != nil {
			return fmt.Errorf("encode JSON: %w", err)
		}
		return nil
	}

	if len(tokens) == 0 {
		fmt.Fprintln(w, "No active tokens.")
		return nil
	}

	// Stable order: most recently used first, then created.
	sort.Slice(tokens, func(i, j int) bool {
		li := lastUsedSortKey(tokens[i])
		lj := lastUsedSortKey(tokens[j])
		if li != lj {
			return li > lj
		}
		return tokens[i].CreatedAt > tokens[j].CreatedAt
	})

	sty := newAuthListStyles(w)
	renderAuthListTable(w, sty, tokens, time.Now())
	return nil
}

// authListStyles holds the lipgloss styles for `entire auth list`. Mirrors the
// approach in activity_render.go: keep style construction tied to color
// detection, and render plain text when color is disabled.
type authListStyles struct {
	colorEnabled bool

	header  lipgloss.Style // bold + dim, used for column headers
	id      lipgloss.Style // yellow accent
	name    lipgloss.Style // bold
	value   lipgloss.Style // default fg for scope/dates (no color)
	dim     lipgloss.Style // "never", "-"
	warning lipgloss.Style // expires-soon
	expired lipgloss.Style // already expired
}

func newAuthListStyles(w io.Writer) authListStyles {
	useColor := shouldUseColor(w)
	s := authListStyles{colorEnabled: useColor}
	if !useColor {
		return s
	}
	s.header = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Bold(true)
	s.id = lipgloss.NewStyle().Foreground(lipgloss.Color("3")) // yellow
	s.name = lipgloss.NewStyle().Bold(true)
	s.value = lipgloss.NewStyle() // default fg
	s.dim = lipgloss.NewStyle().Faint(true)
	s.warning = lipgloss.NewStyle().Foreground(lipgloss.Color("3")) // yellow
	s.expired = lipgloss.NewStyle().Foreground(lipgloss.Color("1")) // red
	return s
}

func (s authListStyles) render(style lipgloss.Style, text string) string {
	if !s.colorEnabled {
		return text
	}
	return style.Render(text)
}

// renderAuthListTable prints a styled, column-aligned table of tokens. Column
// padding is computed via lipgloss.Width — it strips ANSI escapes, so a styled
// cell's visible width matches its plain text. tabwriter can't be used here
// once cells contain ANSI codes.
func renderAuthListTable(w io.Writer, sty authListStyles, tokens []api.Token, now time.Time) {
	headerCells := []string{"ID", "NAME", "SCOPE", "CREATED", "LAST USED", "EXPIRES"}
	header := make([]string, len(headerCells))
	for i, h := range headerCells {
		header[i] = sty.render(sty.header, h)
	}

	rows := make([][]string, 0, len(tokens))
	for _, t := range tokens {
		rows = append(rows, []string{
			sty.render(sty.id, t.ID),
			styleName(sty, t.Name),
			sty.render(sty.value, fallback(t.Scope, placeholderDash)),
			sty.render(sty.value, formatAuthDate(t.CreatedAt)),
			styleLastUsed(sty, t.LastUsedAt, now),
			styleExpires(sty, t.ExpiresAt, now),
		})
	}

	widths := make([]int, len(headerCells))
	for i, h := range header {
		widths[i] = lipgloss.Width(h)
	}
	for _, row := range rows {
		for i, c := range row {
			if cw := lipgloss.Width(c); cw > widths[i] {
				widths[i] = cw
			}
		}
	}

	writeRow(w, header, widths)
	for _, row := range rows {
		writeRow(w, row, widths)
	}
}

func writeRow(w io.Writer, cells []string, widths []int) {
	for i, c := range cells {
		fmt.Fprint(w, c)
		if i < len(cells)-1 {
			fmt.Fprint(w, strings.Repeat(" ", widths[i]-lipgloss.Width(c)+2))
		}
	}
	fmt.Fprintln(w)
}

func styleName(sty authListStyles, name string) string {
	if name == "" {
		return sty.render(sty.dim, placeholderDash)
	}
	return sty.render(sty.name, name)
}

func styleLastUsed(sty authListStyles, lastUsed *string, now time.Time) string {
	if lastUsed == nil {
		return sty.render(sty.dim, lastUsedNever)
	}
	return sty.render(sty.value, formatAuthLastUsed(lastUsed, now))
}

func styleExpires(sty authListStyles, expiresAt string, now time.Time) string {
	formatted := formatAuthDate(expiresAt)
	switch classifyExpiresAt(expiresAt, now) {
	case expiresExpired:
		return sty.render(sty.expired, formatted)
	case expiresSoon:
		return sty.render(sty.warning, formatted)
	case expiresNormal:
		return sty.render(sty.value, formatted)
	}
	return sty.render(sty.value, formatted)
}

func lastUsedSortKey(t api.Token) string {
	if t.LastUsedAt == nil {
		return ""
	}
	return *t.LastUsedAt
}

// formatAuthDate renders an RFC3339 timestamp as YYYY-MM-DD in local time.
func formatAuthDate(s string) string {
	if s == "" {
		return placeholderDash
	}
	if ts, err := time.Parse(time.RFC3339, s); err == nil {
		return ts.Local().Format("2006-01-02")
	}
	return s
}

// formatAuthLastUsed renders a relative "last used" timestamp, with "yesterday"
// and absolute-date branches that the shared formatRelativeDuration helper
// doesn't cover.
func formatAuthLastUsed(s *string, now time.Time) string {
	if s == nil || *s == "" {
		return lastUsedNever
	}
	ts, err := time.Parse(time.RFC3339, *s)
	if err != nil {
		return *s
	}
	delta := now.Sub(ts)
	switch {
	case delta < 0, delta >= 30*24*time.Hour:
		return ts.Local().Format("2006-01-02")
	case delta >= 24*time.Hour && delta < 48*time.Hour:
		return "yesterday"
	default:
		return formatRelativeDuration(delta)
	}
}

type expiresState int

const (
	expiresNormal expiresState = iota
	expiresSoon
	expiresExpired
)

// classifyExpiresAt classifies an RFC3339 expires-at relative to now. Used to
// color the EXPIRES column so tokens worth rotating stand out.
func classifyExpiresAt(s string, now time.Time) expiresState {
	if s == "" {
		return expiresNormal
	}
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return expiresNormal
	}
	delta := ts.Sub(now)
	switch {
	case delta <= 0:
		return expiresExpired
	case delta < 7*24*time.Hour:
		return expiresSoon
	default:
		return expiresNormal
	}
}

func fallback(s, alt string) string {
	if strings.TrimSpace(s) == "" {
		return alt
	}
	return s
}

// --- revoke -----------------------------------------------------------------

func newAuthRevokeCmd() *cobra.Command {
	var revokeCurrent bool
	cmd := &cobra.Command{
		Use:   "revoke [id]",
		Short: "Revoke an API token by id",
		Long:  "Revoke a specific API token. Use --current to revoke the token used by this CLI (equivalent to 'entire logout').",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := ""
			if len(args) == 1 {
				id = args[0]
			}
			if id == "" && !revokeCurrent {
				return cmd.Help()
			}
			if id != "" && revokeCurrent {
				return errors.New("cannot use both <id> and --current")
			}
			return runAuthRevoke(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(),
				auth.NewStore(), defaultRevokeTokenByID, defaultRevokeCurrentToken,
				api.BaseURL(), id, revokeCurrent)
		},
	}
	cmd.Flags().BoolVar(&revokeCurrent, "current", false, "Revoke the token used by this CLI and remove the local copy")
	return cmd
}

func defaultRevokeTokenByID(ctx context.Context, callerToken, id string) error {
	return api.NewClient(callerToken).RevokeToken(ctx, id) //nolint:wrapcheck // RevokeToken already wraps with action context
}

func runAuthRevoke(
	ctx context.Context,
	outW, errW io.Writer,
	store logoutTokenStore,
	revokeByID authTokenRevoker,
	revokeCurrent logoutRevokeFunc,
	baseURL, id string,
	current bool,
) error {
	token, err := store.GetToken(baseURL)
	if err != nil {
		return fmt.Errorf("read keychain: %w", err)
	}
	if token == "" {
		return fmt.Errorf("not logged in to %s; run 'entire login' first", baseURL)
	}

	if current {
		// Revoking our own token is just logout — reuse that path so behavior
		// stays identical (best-effort revoke + local delete).
		return runLogout(ctx, outW, errW, store, revokeCurrent, baseURL)
	}

	if err := revokeByID(ctx, token, id); err != nil {
		return err
	}
	fmt.Fprintf(outW, "Revoked token %s.\n", id)
	return nil
}
