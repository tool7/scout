package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/readcube/readcube-scout/internal/config"
	syncpkg "github.com/readcube/readcube-scout/internal/sync"
)

func newSyncCmd() *cobra.Command {
	var (
		project string
		source  string
		full    bool
	)

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync Git, Jira, and code data into the local knowledge base",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateSource(source); err != nil {
				return err
			}
			return runSync(cmd.Context(), project, source, full)
		},
	}

	cmd.Flags().StringVarP(&project, "project", "p", "", "Only sync the named project")
	cmd.Flags().StringVarP(&source, "source", "s", "all", "Only sync a specific source: git | jira | code | all")
	cmd.Flags().BoolVarP(&full, "full", "f", false, "Force a full Jira/code re-fetch instead of incremental (no-op for git)")

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

	fetched := syncpkg.NewFetchSet()

	for _, project := range projects {
		if source == "git" || source == "all" {
			if _, err := syncpkg.SyncGitProject(ctx, rt.db, project, fetched); err != nil {
				return err
			}
		}
		if source == "jira" || source == "all" {
			if _, err := syncpkg.SyncJiraProject(ctx, rt.db, project, rt.cfg.Jira, syncpkg.JiraSyncOptions{Full: full}); err != nil {
				return err
			}
		}
		if source == "code" || source == "all" {
			if _, err := syncpkg.SyncCodeProject(ctx, rt.db, project, fetched, syncpkg.CodeSyncOptions{Full: full}); err != nil {
				return err
			}
		}
	}

	return nil
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
