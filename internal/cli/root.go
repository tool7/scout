package cli

import (
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"scout/internal/config"
	"scout/internal/db"
	"scout/internal/logger"
)

//go:embed instructions.md
var instructionsContent string

func NewRootCmd(version string) *cobra.Command {
	var showInstructions bool

	root := &cobra.Command{
		Use:           "scout",
		Short:         "Query and sync your local Scout knowledge base (Git commits, Jira tickets, source code)",
		Long:          "Query and sync your local Scout knowledge base (Git commits, Jira tickets, source code).\n\nRun `scout --instructions` for the full usage reference, suitable for both humans and AI agents.",
		Version:       "v" + version,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if showInstructions {
				fmt.Fprint(os.Stdout, instructionsContent)
				return nil
			}
			return cmd.Help()
		},
	}

	root.SetVersionTemplate("{{.Version}}\n")

	root.Flags().BoolVar(&showInstructions, "instructions", false, "Print the full CLI usage reference (humans + AI agents) and exit")

	root.AddCommand(newSearchCmd())
	root.AddCommand(newHistoryCmd())
	root.AddCommand(newRelatedCmd())
	root.AddCommand(newSyncCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newJiraLoginCmd())
	root.AddCommand(newJiraLogoutCmd())

	return root
}

type silentError struct{}

func (silentError) Error() string { return "" }

var errSilent = silentError{}

func Execute(version string) int {
	root := NewRootCmd(version)
	if err := root.Execute(); err != nil {
		if _, ok := err.(silentError); !ok {
			logger.Error(err.Error())
		}
		return 1
	}
	return 0
}

type runtime struct {
	cfg *config.Config
	db  *sql.DB
}

func openRuntime() (*runtime, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(cfg.DataDir, "knowledge.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		return nil, err
	}
	return &runtime{cfg: cfg, db: conn}, nil
}

func (r *runtime) close() {
	if r.db != nil {
		r.db.Close()
	}
}

func writeStdout(s string) {
	fmt.Fprintln(os.Stdout, s)
}

func writeStderr(s string) {
	fmt.Fprintln(os.Stderr, s)
}
