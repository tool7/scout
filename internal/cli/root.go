package cli

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/readcube/readcube-scout/internal/config"
	"github.com/readcube/readcube-scout/internal/db"
	"github.com/readcube/readcube-scout/internal/logger"
)

const Version = "0.1.0"

func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "readcube-scout",
		Short:         "Query and sync the local ReadCube Scout knowledge base (Git commits, Jira tickets, source code)",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(newSearchCmd())
	root.AddCommand(newHistoryCmd())
	root.AddCommand(newRelatedCmd())
	root.AddCommand(newSyncCmd())
	root.AddCommand(newStatusCmd())

	return root
}

type silentError struct{}

func (silentError) Error() string { return "" }

var errSilent = silentError{}

func Execute() int {
	root := NewRootCmd()
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
