package cli

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/spf13/cobra"

	"scout/internal/db"
	"scout/internal/format"
	"scout/internal/fts"
)

type rankedEntry struct {
	kind   string
	rank   float64
	commit *db.CommitRow
	ticket *db.TicketRow
	file   *db.FileRow
}

func newSearchCmd() *cobra.Command {
	var (
		project string
		source  string
		limit   int
	)

	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Broad full-text search across Git commits, Jira tickets, and source-code files",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateSource(source); err != nil {
				return err
			}
			if err := validateRange("--limit", limit, 1, 50); err != nil {
				return err
			}
			return runSearch(args[0], project, source, limit)
		},
	}

	cmd.Flags().StringVarP(&project, "project", "p", "", "Only search within one project")
	cmd.Flags().StringVarP(&source, "source", "s", "all", "Which source to search: git | jira | code | all")
	cmd.Flags().IntVarP(&limit, "limit", "l", 20, "Maximum results to return (1-50)")

	return cmd
}

func runSearch(query, project, source string, limit int) error {
	naturalQuery := fts.ToQuery(query, fts.ModeNatural)
	codeQuery := fts.ToQuery(query, fts.ModeCode)

	if naturalQuery == "" && codeQuery == "" {
		return emptyQueryError(query)
	}

	rt, err := openRuntime()
	if err != nil {
		return err
	}
	defer rt.close()

	var commits []db.CommitRow
	if naturalQuery != "" && source != "jira" && source != "code" {
		commits, err = db.SearchCommits(rt.db, naturalQuery, db.CommitSearchOptions{
			Project: project,
			Limit:   limit,
		})
		if err != nil {
			return err
		}
	}

	var tickets []db.TicketRow
	if naturalQuery != "" && source != "git" && source != "code" {
		tickets, err = db.SearchTickets(rt.db, naturalQuery, db.TicketSearchOptions{
			Project: project,
			Status:  db.TicketStatusAll,
			Limit:   limit,
		})
		if err != nil {
			return err
		}
	}

	var files []db.FileRow
	if codeQuery != "" && source != "git" && source != "jira" {
		files, err = db.SearchFiles(rt.db, codeQuery, db.FileSearchOptions{
			Project: project,
			Limit:   limit,
		})
		if err != nil {
			return err
		}
	}

	entries := make([]rankedEntry, 0, len(commits)+len(tickets)+len(files))
	for i := range commits {
		entries = append(entries, rankedEntry{kind: "commit", rank: commits[i].Rank, commit: &commits[i]})
	}
	for i := range tickets {
		entries = append(entries, rankedEntry{kind: "ticket", rank: tickets[i].Rank, ticket: &tickets[i]})
	}
	for i := range files {
		entries = append(entries, rankedEntry{kind: "file", rank: files[i].Rank, file: &files[i]})
	}

	sort.SliceStable(entries, func(i, j int) bool { return entries[i].rank < entries[j].rank })
	if len(entries) > limit {
		entries = entries[:limit]
	}

	if len(entries) == 0 {
		writeStdout(`No matches for "` + query + `".`)
		return nil
	}

	header := `Found ` + strconv.Itoa(len(entries)) + ` result(s) for "` + query + `"`
	if project != "" {
		header += ` in project "` + project + `"`
	}
	if source != "all" {
		header += ` (source: ` + source + `)`
	}
	header += ":"

	out := header
	for i, entry := range entries {
		body := ""
		switch {
		case entry.kind == "commit" && entry.commit != nil:
			body = format.Commit(*entry.commit)
		case entry.kind == "ticket" && entry.ticket != nil:
			body = format.Ticket(*entry.ticket, false)
		case entry.kind == "file" && entry.file != nil:
			body = format.File(*entry.file)
		}
		out += "\n\n" + strconv.Itoa(i+1) + ". " + body
	}

	writeStdout(out)
	return nil
}

func emptyQueryError(input string) error {
	writeStderr(fmt.Sprintf(`No usable search terms in "%s".`, input))
	return errSilent
}
