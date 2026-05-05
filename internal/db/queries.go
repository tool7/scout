package db

import (
	"database/sql"
	"fmt"
)

type CommitRow struct {
	ID      string
	Project string
	Author  string
	Date    string
	Message string
	Body    string
	Files   string
	Rank    float64
}

type TicketRow struct {
	ID          string
	Project     string
	Summary     string
	Description string
	Type        string
	Status      string
	Resolution  string
	CreatedAt   string
	UpdatedAt   string
	Reporter    string
	Assignee    string
	Labels      string
	Comments    string
	Rank        float64
}

type FileRow struct {
	Project  string
	Path     string
	Language string
	Snippet  string
	Rank     float64
}

type SyncStateRow struct {
	Project     string
	Source      string
	LastSynced  string
	CommitCount sql.NullInt64
	TicketCount sql.NullInt64
	FileCount   sql.NullInt64
}

type CommitSearchOptions struct {
	Project string
	Since   string
	Limit   int
}

type TicketStatusFilter int

const (
	TicketStatusAll TicketStatusFilter = iota
	TicketStatusOpen
	TicketStatusResolved
)

type TicketSearchOptions struct {
	Project string
	Since   string
	Status  TicketStatusFilter
	Limit   int
}

type FileSearchOptions struct {
	Project string
	Limit   int
}

func SearchCommits(db *sql.DB, ftsQuery string, opts CommitSearchOptions) ([]CommitRow, error) {
	clauses := []string{"commits_fts MATCH ?"}
	args := []any{ftsQuery}

	if opts.Project != "" {
		clauses = append(clauses, "c.project = ? COLLATE NOCASE")
		args = append(args, opts.Project)
	}
	if opts.Since != "" {
		clauses = append(clauses, "c.date >= ?")
		args = append(args, opts.Since)
	}

	query := `
		SELECT c.id, c.project, c.author, c.date, c.message,
		       COALESCE(c.body, ''), COALESCE(c.files, ''),
		       bm25(commits_fts) AS rank
		FROM commits_fts
		JOIN commits c ON c.rowid = commits_fts.rowid
		WHERE ` + joinAnd(clauses) + `
		ORDER BY rank
		LIMIT ?
	`
	args = append(args, opts.Limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("commit search query failed: %w", err)
	}
	defer rows.Close()

	var result []CommitRow
	for rows.Next() {
		var r CommitRow
		if err := rows.Scan(&r.ID, &r.Project, &r.Author, &r.Date, &r.Message, &r.Body, &r.Files, &r.Rank); err != nil {
			return nil, fmt.Errorf("commit search scan failed: %w", err)
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("commit search iteration failed: %w", err)
	}
	return result, nil
}

func SearchTickets(db *sql.DB, ftsQuery string, opts TicketSearchOptions) ([]TicketRow, error) {
	clauses := []string{"tickets_fts MATCH ?"}
	args := []any{ftsQuery}

	if opts.Project != "" {
		clauses = append(clauses, "t.project = ? COLLATE NOCASE")
		args = append(args, opts.Project)
	}
	if opts.Since != "" {
		clauses = append(clauses, "t.updated_at >= ?")
		args = append(args, opts.Since)
	}
	switch opts.Status {
	case TicketStatusOpen:
		clauses = append(clauses, "(t.resolution IS NULL OR t.resolution = '')")
	case TicketStatusResolved:
		clauses = append(clauses, "(t.resolution IS NOT NULL AND t.resolution != '')")
	}

	query := `
		SELECT t.id, t.project, t.summary, COALESCE(t.description, ''),
		       COALESCE(t.type, ''), COALESCE(t.status, ''), COALESCE(t.resolution, ''),
		       COALESCE(t.created_at, ''), COALESCE(t.updated_at, ''),
		       COALESCE(t.reporter, ''), COALESCE(t.assignee, ''),
		       COALESCE(t.labels, ''), COALESCE(t.comments, ''),
		       bm25(tickets_fts) AS rank
		FROM tickets_fts
		JOIN tickets t ON t.rowid = tickets_fts.rowid
		WHERE ` + joinAnd(clauses) + `
		ORDER BY rank
		LIMIT ?
	`
	args = append(args, opts.Limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("ticket search query failed: %w", err)
	}
	defer rows.Close()

	var result []TicketRow
	for rows.Next() {
		var r TicketRow
		if err := rows.Scan(
			&r.ID, &r.Project, &r.Summary, &r.Description,
			&r.Type, &r.Status, &r.Resolution,
			&r.CreatedAt, &r.UpdatedAt,
			&r.Reporter, &r.Assignee,
			&r.Labels, &r.Comments,
			&r.Rank,
		); err != nil {
			return nil, fmt.Errorf("ticket search scan failed: %w", err)
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ticket search iteration failed: %w", err)
	}
	return result, nil
}

func SearchFiles(db *sql.DB, ftsQuery string, opts FileSearchOptions) ([]FileRow, error) {
	clauses := []string{"files_fts MATCH ?"}
	args := []any{ftsQuery}

	if opts.Project != "" {
		clauses = append(clauses, "f.project = ? COLLATE NOCASE")
		args = append(args, opts.Project)
	}

	query := `
		SELECT f.project, f.path, COALESCE(f.language, ''),
		       snippet(files_fts, 1, '«', '»', '…', 16) AS snippet,
		       bm25(files_fts) AS rank
		FROM files_fts
		JOIN files f ON f.rowid = files_fts.rowid
		WHERE ` + joinAnd(clauses) + `
		ORDER BY rank
		LIMIT ?
	`
	args = append(args, opts.Limit)

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("file search query failed: %w", err)
	}
	defer rows.Close()

	var result []FileRow
	for rows.Next() {
		var r FileRow
		if err := rows.Scan(&r.Project, &r.Path, &r.Language, &r.Snippet, &r.Rank); err != nil {
			return nil, fmt.Errorf("file search scan failed: %w", err)
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("file search iteration failed: %w", err)
	}
	return result, nil
}

func LastSynced(db *sql.DB, project, source string) (string, bool, error) {
	var lastSynced string
	err := db.QueryRow(
		"SELECT last_synced FROM sync_state WHERE project = ? AND source = ?",
		project, source,
	).Scan(&lastSynced)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("failed to read last_synced for %s/%s: %w", project, source, err)
	}
	return lastSynced, true, nil
}

func SyncState(db *sql.DB) ([]SyncStateRow, error) {
	rows, err := db.Query(
		"SELECT project, source, last_synced, commit_count, ticket_count, file_count FROM sync_state ORDER BY project, source",
	)
	if err != nil {
		return nil, fmt.Errorf("failed to read sync_state: %w", err)
	}
	defer rows.Close()

	var result []SyncStateRow
	for rows.Next() {
		var r SyncStateRow
		if err := rows.Scan(&r.Project, &r.Source, &r.LastSynced, &r.CommitCount, &r.TicketCount, &r.FileCount); err != nil {
			return nil, fmt.Errorf("failed to scan sync_state row: %w", err)
		}
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate sync_state: %w", err)
	}
	return result, nil
}

func Totals(db *sql.DB) (commits, tickets int, err error) {
	if err := db.QueryRow("SELECT COUNT(*) FROM commits").Scan(&commits); err != nil {
		return 0, 0, fmt.Errorf("failed to count commits: %w", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM tickets").Scan(&tickets); err != nil {
		return 0, 0, fmt.Errorf("failed to count tickets: %w", err)
	}
	return commits, tickets, nil
}

func joinAnd(clauses []string) string {
	out := clauses[0]
	for _, c := range clauses[1:] {
		out += " AND " + c
	}
	return out
}
