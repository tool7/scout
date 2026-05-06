package cli

import (
	"github.com/spf13/cobra"

	"scout/internal/config"
	"scout/internal/oauth"
)

func newJiraLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "jira-login",
		Short: "Authenticate to Jira via OAuth 2.0 (3LO) in your browser",
		Long: "Open a browser tab so you can grant Scout read access to your Jira site.\n" +
			"On success, OAuth tokens are saved to <dataDir>/" + oauth.TokenFileName + " and " +
			"refreshed automatically on subsequent `scout sync` runs.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			tokens, err := oauth.Login(cmd.Context(), cfg.DataDir, cfg.Jira.Host)
			if err != nil {
				return err
			}
			writeStdout("Logged in to " + cfg.Jira.Host + " (cloudId=" + tokens.CloudID + ")")
			writeStdout("Token saved to " + oauth.TokenPath(cfg.DataDir))
			return nil
		},
	}
}

func newJiraLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "jira-logout",
		Short: "Forget the locally stored Jira OAuth tokens",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if err := oauth.Delete(cfg.DataDir); err != nil {
				return err
			}
			writeStdout("Removed " + oauth.TokenPath(cfg.DataDir))
			return nil
		},
	}
}
