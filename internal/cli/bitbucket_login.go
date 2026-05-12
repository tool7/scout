package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"scout/internal/config"
	syncpkg "scout/internal/sync"
)

func newBitbucketLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bitbucket-login",
		Short: "Store an Atlassian email + Bitbucket API token for PR sync",
		Long: "Prompt for your Atlassian account email and a Bitbucket API token, validate the pair " +
			"against the Bitbucket API, and save them to <dataDir>/" + syncpkg.BitbucketTokenFileName + " with 0600 permissions.\n\n" +
			"Create an API token at https://id.atlassian.com/manage-profile/security/api-tokens " +
			"→ 'Create API token with scopes' → pick 'Bitbucket' as the app, then assign at least the " +
			"'read:pullrequest:bitbucket' and 'read:user:bitbucket' scopes.\n\n" +
			"App Passwords are deprecated by Atlassian and stop working on June 9, 2026.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if !anyProjectHasBitbucket(cfg) {
				return fmt.Errorf("No project has bitbucketRepo set; add one to scout.config.json before running bitbucket-login.")
			}

			email, err := promptLine("Enter your Atlassian account email: ")
			if err != nil {
				return err
			}
			email = strings.TrimSpace(email)
			if email == "" {
				return fmt.Errorf("No email provided.")
			}

			apiToken, err := promptPassword("Enter your Bitbucket API token: ")
			if err != nil {
				return err
			}
			apiToken = strings.TrimSpace(apiToken)
			if apiToken == "" {
				return fmt.Errorf("No API token provided.")
			}

			displayName, err := syncpkg.ValidateBitbucketCreds(cmd.Context(), email, apiToken)
			if err != nil {
				return err
			}
			if err := syncpkg.SaveBitbucketCreds(cfg.DataDir, email, apiToken); err != nil {
				return err
			}
			writeStdout("Logged in to Bitbucket as " + displayName)
			writeStdout("Credentials saved to " + syncpkg.BitbucketTokenPath(cfg.DataDir))
			return nil
		},
	}
}

func newBitbucketLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "bitbucket-logout",
		Short: "Forget the locally stored Bitbucket API token",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if err := syncpkg.DeleteBitbucketCreds(cfg.DataDir); err != nil {
				return err
			}
			writeStdout("Removed " + syncpkg.BitbucketTokenPath(cfg.DataDir))
			return nil
		},
	}
}

func anyProjectHasBitbucket(cfg *config.Config) bool {
	for _, p := range cfg.Projects {
		if p.HasBitbucket() {
			return true
		}
	}
	return false
}

func promptLine(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read input: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func promptPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		raw, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", fmt.Errorf("failed to read password: %w", err)
		}
		return string(raw), nil
	}
	// Fallback for non-TTY stdin (e.g. piped input in CI scripts):
	// read a single line. Echo is unavoidable here, but in this mode
	// the caller has explicitly chosen to feed credentials in.
	var line string
	if _, err := fmt.Fscanln(os.Stdin, &line); err != nil {
		return "", fmt.Errorf("failed to read input: %w", err)
	}
	return line, nil
}
