package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"scout/internal/config"
	"scout/internal/githubauth"
	syncpkg "scout/internal/sync"
)

func newGitHubLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "github-login",
		Short: "Authorize scout to read your GitHub PRs via OAuth Device Flow",
		Long: "Open a browser tab so you can grant scout read access to your GitHub repositories.\n" +
			"Scout prints a short user code, attempts to open the verification URL in your default " +
			"browser, and polls until you approve. On success the access token is saved to " +
			"<dataDir>/" + syncpkg.GitHubTokenFileName + " with 0600 permissions.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if !anyProjectHasGitHub(cfg) {
				return fmt.Errorf("No project has githubRepo set; add one to scout.config.json before running github-login.")
			}

			token, err := githubauth.Login(cmd.Context())
			if err != nil {
				return err
			}

			username, err := syncpkg.ValidateGitHubToken(cmd.Context(), token)
			if err != nil {
				return err
			}
			if err := syncpkg.SaveGitHubToken(cfg.DataDir, token); err != nil {
				return err
			}
			writeStdout("Logged in to GitHub as " + username)
			writeStdout("Token saved to " + syncpkg.GitHubTokenPath(cfg.DataDir))
			return nil
		},
	}
}

func newGitHubLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "github-logout",
		Short: "Forget the locally stored GitHub OAuth token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if err := syncpkg.DeleteGitHubToken(cfg.DataDir); err != nil {
				return err
			}
			writeStdout("Removed " + syncpkg.GitHubTokenPath(cfg.DataDir))
			return nil
		},
	}
}

func anyProjectHasGitHub(cfg *config.Config) bool {
	for _, p := range cfg.Projects {
		if p.HasGitHub() {
			return true
		}
	}
	return false
}
