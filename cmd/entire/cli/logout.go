package cli

import (
	"fmt"

	apiurl "github.com/entireio/cli/cmd/entire/cli/api"
	"github.com/entireio/cli/cmd/entire/cli/auth"
	"github.com/spf13/cobra"
)

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Log out of Entire",
		RunE: func(cmd *cobra.Command, _ []string) error {
			store := auth.NewStore()
			baseURL := apiurl.BaseURL()

			if err := store.DeleteToken(baseURL); err != nil {
				return fmt.Errorf("remove auth token: %w", err)
			}

			fmt.Fprintln(cmd.OutOrStdout(), "Logged out.")
			return nil
		},
	}
}
