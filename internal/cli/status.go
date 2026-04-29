package cli

import (
	"database/sql"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"scout/internal/config"
	"scout/internal/db"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show last sync time and record counts per project / source",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus()
		},
	}
}

func runStatus() error {
	rt, err := openRuntime()
	if err != nil {
		return err
	}
	defer rt.close()

	commits, tickets, err := db.Totals(rt.db)
	if err != nil {
		return err
	}
	states, err := db.SyncState(rt.db)
	if err != nil {
		return err
	}

	dbPath := filepath.Join(rt.cfg.DataDir, "knowledge.db")
	printStatus(rt.cfg, dbPath, commits, tickets, states)
	return nil
}

func printStatus(cfg *config.Config, dbPath string, commits, tickets int, states []db.SyncStateRow) {
	writeStdout("Database:  " + dbPath)
	writeStdout("Totals:    " + strconv.Itoa(commits) + " commit(s), " + strconv.Itoa(tickets) + " ticket(s)")
	writeStdout("")

	byProject := make(map[string]map[string]db.SyncStateRow)
	for _, state := range states {
		sources, ok := byProject[state.Project]
		if !ok {
			sources = make(map[string]db.SyncStateRow)
			byProject[state.Project] = sources
		}
		sources[state.Source] = state
	}

	rows := [][]string{{"project", "source", "last synced", "records"}}
	for _, project := range cfg.Projects {
		for _, source := range []string{"git", "jira", "code"} {
			synced := "(never)"
			records := "-"
			if state, ok := byProject[project.Name][source]; ok {
				synced = state.LastSynced
				switch source {
				case "git":
					records = nullableCount(state.CommitCount)
				case "jira":
					records = nullableCount(state.TicketCount)
				case "code":
					records = nullableCount(state.FileCount)
				}
			}
			rows = append(rows, []string{project.Name, source, synced, records})
		}
	}

	widths := make([]int, len(rows[0]))
	for _, row := range rows {
		for i, cell := range row {
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	for index, row := range rows {
		parts := make([]string, len(row))
		for i, cell := range row {
			parts[i] = padEnd(cell, widths[i])
		}
		writeStdout(strings.Join(parts, "  "))
		if index == 0 {
			dashes := make([]string, len(widths))
			for i, w := range widths {
				dashes[i] = strings.Repeat("-", w)
			}
			writeStdout(strings.Join(dashes, "  "))
		}
	}
}

func nullableCount(v sql.NullInt64) string {
	if !v.Valid {
		return "0"
	}
	return strconv.FormatInt(v.Int64, 10)
}

func padEnd(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}
