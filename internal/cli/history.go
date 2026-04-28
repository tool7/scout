package cli

import (
	"sort"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/readcube/readcube-scout/internal/db"
	"github.com/readcube/readcube-scout/internal/format"
	"github.com/readcube/readcube-scout/internal/fts"
)

type timelineEvent struct {
	kind   string
	rank   float64
	date   string
	commit *db.CommitRow
	ticket *db.TicketRow
}

func newHistoryCmd() *cobra.Command {
	var (
		project string
		since   string
		limit   int
	)

	cmd := &cobra.Command{
		Use:   "history <topic>",
		Short: "Unified chronological timeline for a topic (commits + tickets, oldest first)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateRange("--limit", limit, 1, 100); err != nil {
				return err
			}
			return runHistory(args[0], project, since, limit)
		},
	}

	cmd.Flags().StringVarP(&project, "project", "p", "", "Only search within one project")
	cmd.Flags().StringVar(&since, "since", "", "ISO 8601 lower bound (e.g. 2024-01-01)")
	cmd.Flags().IntVarP(&limit, "limit", "l", 30, "Maximum results to return (1-100)")

	return cmd
}

func runHistory(topic, project, since string, limit int) error {
	ftsQuery := fts.ToQuery(topic, fts.ModeNatural)
	if ftsQuery == "" {
		return emptyQueryError(topic)
	}

	rt, err := openRuntime()
	if err != nil {
		return err
	}
	defer rt.close()

	commits, err := db.SearchCommits(rt.db, ftsQuery, db.CommitSearchOptions{
		Project: project,
		Since:   since,
		Limit:   limit,
	})
	if err != nil {
		return err
	}
	tickets, err := db.SearchTickets(rt.db, ftsQuery, db.TicketSearchOptions{
		Project: project,
		Since:   since,
		Status:  db.TicketStatusAll,
		Limit:   limit,
	})
	if err != nil {
		return err
	}

	events := make([]timelineEvent, 0, len(commits)+len(tickets))
	for i := range commits {
		events = append(events, timelineEvent{kind: "commit", rank: commits[i].Rank, date: commits[i].Date, commit: &commits[i]})
	}
	for i := range tickets {
		date := tickets[i].UpdatedAt
		if date == "" {
			date = tickets[i].CreatedAt
		}
		events = append(events, timelineEvent{kind: "ticket", rank: tickets[i].Rank, date: date, ticket: &tickets[i]})
	}

	sort.SliceStable(events, func(i, j int) bool { return events[i].rank < events[j].rank })
	if len(events) > limit {
		events = events[:limit]
	}

	sort.SliceStable(events, func(i, j int) bool { return events[i].date < events[j].date })

	if len(events) == 0 {
		writeStdout(`No matches for "` + topic + `".`)
		return nil
	}

	header := `Timeline for "` + topic + `" — ` + strconv.Itoa(len(events)) + ` event(s), oldest first`
	if project != "" {
		header += ` (project: ` + project + `)`
	}
	if since != "" {
		sinceDay := since
		if len(since) >= 10 {
			sinceDay = since[:10]
		}
		header += ` (since ` + sinceDay + `)`
	}
	header += ":"

	out := header
	for _, event := range events {
		day := "????-??-??"
		if len(event.date) >= 10 {
			day = event.date[:10]
		}
		body := ""
		switch {
		case event.kind == "commit" && event.commit != nil:
			body = format.Commit(*event.commit)
		case event.ticket != nil:
			body = format.Ticket(*event.ticket, false)
		}
		out += "\n\n— " + day + " —\n" + body
	}

	writeStdout(out)
	return nil
}
