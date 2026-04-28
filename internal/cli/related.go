package cli

import (
	"strconv"

	"github.com/spf13/cobra"

	"github.com/readcube/readcube-scout/internal/db"
	"github.com/readcube/readcube-scout/internal/format"
	"github.com/readcube/readcube-scout/internal/fts"
)

func newRelatedCmd() *cobra.Command {
	var (
		project string
		status  string
		limit   int
	)

	cmd := &cobra.Command{
		Use:   "related <description>",
		Short: "Find Jira tickets similar to a bug or behaviour description",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateStatus(status); err != nil {
				return err
			}
			if err := validateRange("--limit", limit, 1, 30); err != nil {
				return err
			}
			return runRelated(args[0], project, status, limit)
		},
	}

	cmd.Flags().StringVarP(&project, "project", "p", "", "Only search within one Jira project")
	cmd.Flags().StringVarP(&status, "status", "s", "all", "Filter by status: open | resolved | all")
	cmd.Flags().IntVarP(&limit, "limit", "l", 10, "Maximum results to return (1-30)")

	return cmd
}

func runRelated(description, project, status string, limit int) error {
	ftsQuery := fts.ToQuery(description, fts.ModeNatural)
	if ftsQuery == "" {
		return emptyQueryError(description)
	}

	rt, err := openRuntime()
	if err != nil {
		return err
	}
	defer rt.close()

	statusFilter := db.TicketStatusAll
	switch status {
	case "open":
		statusFilter = db.TicketStatusOpen
	case "resolved":
		statusFilter = db.TicketStatusResolved
	}

	tickets, err := db.SearchTickets(rt.db, ftsQuery, db.TicketSearchOptions{
		Project: project,
		Status:  statusFilter,
		Limit:   limit,
	})
	if err != nil {
		return err
	}

	if len(tickets) == 0 {
		writeStdout(`No matches for "` + description + `".`)
		return nil
	}

	header := `Found ` + strconv.Itoa(len(tickets)) + ` related ticket(s)`
	if status != "all" {
		header += ` (status: ` + status + `)`
	}
	if project != "" {
		header += ` in project "` + project + `"`
	}
	header += ":"

	out := header
	for i, ticket := range tickets {
		out += "\n\n" + strconv.Itoa(i+1) + ". " + format.Ticket(ticket, true)
	}

	writeStdout(out)
	return nil
}
