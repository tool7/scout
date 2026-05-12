package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"scout/internal/config"
	syncpkg "scout/internal/sync"
)

func newSyncCmd() *cobra.Command {
	var (
		project string
		source  string
		full    bool
	)

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync Git, Jira, code, and PR data into the local knowledge base",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateSource(source); err != nil {
				return err
			}
			return runSync(cmd.Context(), project, source, full)
		},
	}

	cmd.Flags().StringVarP(&project, "project", "p", "", "Only sync the named project")
	cmd.Flags().StringVarP(&source, "source", "s", "all", "Only sync a specific source: git | jira | code | prs | all")
	cmd.Flags().BoolVarP(&full, "full", "f", false, "Force a full Jira/code/PR re-fetch instead of incremental (no-op for git)")

	return cmd
}

func runSync(ctx context.Context, projectName, source string, full bool) error {
	if ctx == nil {
		ctx = context.Background()
	}

	rt, err := openRuntime()
	if err != nil {
		return err
	}
	defer rt.close()

	projects, err := selectProjects(rt.cfg, projectName)
	if err != nil {
		return err
	}

	if source == "jira" {
		if err := requireJiraConfigured(rt.cfg, projects, projectName); err != nil {
			return err
		}
	}
	if source == "prs" {
		if err := requirePRConfigured(projects, projectName); err != nil {
			return err
		}
	}

	fetched := syncpkg.NewFetchSet()

	for _, project := range projects {
		if source == "git" || source == "all" {
			if _, err := syncpkg.SyncGitProject(ctx, rt.db, project, fetched); err != nil {
				return err
			}
		}
		if (source == "jira" || source == "all") && shouldSyncJira(rt.cfg, project) {
			if _, err := syncpkg.SyncJiraProject(ctx, rt.db, project, rt.cfg.DataDir, syncpkg.JiraSyncOptions{Full: full}); err != nil {
				return err
			}
		}
		if source == "code" || source == "all" {
			if _, err := syncpkg.SyncCodeProject(ctx, rt.db, project, fetched, syncpkg.CodeSyncOptions{Full: full}); err != nil {
				return err
			}
		}
		if (source == "prs" || source == "all") && project.HasPRSource() {
			if project.HasGitHub() {
				if _, err := syncpkg.SyncGitHubProject(ctx, rt.db, project, rt.cfg.DataDir, syncpkg.PRSyncOptions{Full: full}); err != nil {
					return err
				}
			}
			if project.HasBitbucket() {
				if _, err := syncpkg.SyncBitbucketProject(ctx, rt.db, project, rt.cfg.DataDir, syncpkg.PRSyncOptions{Full: full}); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// requirePRConfigured mirrors requireJiraConfigured: when the user
// asks for `--source prs` but nothing would be synced, explain exactly
// why so they can fix the config rather than seeing a silent no-op.
func requirePRConfigured(projects []config.Project, projectName string) error {
	if projectName != "" {
		if len(projects) == 1 && !projects[0].HasPRSource() {
			return fmt.Errorf("Project %q has no githubRepo or bitbucketRepo set; cannot sync PRs for it.", projectName)
		}
		return nil
	}
	for _, p := range projects {
		if p.HasPRSource() {
			return nil
		}
	}
	return fmt.Errorf("No projects have githubRepo or bitbucketRepo set; nothing to sync from PRs.")
}

// shouldSyncJira reports whether a project participates in Jira sync.
// Both the global jira.host and the per-project jiraProjectKey must
// be set; otherwise we skip silently when the user asked for `--source
// all`. Validation guarantees we won't see a project key without a
// host, so checking JiraProjectKey is sufficient here.
func shouldSyncJira(cfg *config.Config, project config.Project) bool {
	return cfg.Jira.Configured() && project.JiraProjectKey != ""
}

// requireJiraConfigured emits a precise error when the user asks for
// `--source jira` but no project would actually be synced. It
// distinguishes the global-not-configured, single-project-not-set, and
// no-projects-at-all cases so the message tells the user what to fix.
func requireJiraConfigured(cfg *config.Config, projects []config.Project, projectName string) error {
	if !cfg.Jira.Configured() {
		return fmt.Errorf("Jira is not configured. Set jira.host in scout.config.json to use Jira features.")
	}
	if projectName != "" {
		if len(projects) == 1 && projects[0].JiraProjectKey == "" {
			return fmt.Errorf("Project %q has no jiraProjectKey set; cannot sync Jira for it.", projectName)
		}
		return nil
	}
	for _, p := range projects {
		if p.JiraProjectKey != "" {
			return nil
		}
	}
	return fmt.Errorf("No projects have jiraProjectKey set; nothing to sync from Jira.")
}

func selectProjects(cfg *config.Config, name string) ([]config.Project, error) {
	if name == "" {
		return cfg.Projects, nil
	}
	for _, p := range cfg.Projects {
		if p.Name == name {
			return []config.Project{p}, nil
		}
	}
	configured := ""
	for i, p := range cfg.Projects {
		if i > 0 {
			configured += ", "
		}
		configured += p.Name
	}
	return nil, fmt.Errorf(`No project named %q found. Configured projects: %s`, name, configured)
}
