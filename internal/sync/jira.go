package sync

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"scout/internal/adf"
	"scout/internal/config"
	dbpkg "scout/internal/db"
	"scout/internal/logger"
	"scout/internal/oauth"
)

const (
	incrementalOverlapMinutes = 10
	searchPageSize            = 100
	jiraTimeout               = 30 * time.Second
)

var searchFields = []string{
	"summary", "description", "issuetype", "status", "resolution",
	"created", "updated", "reporter", "assignee", "labels", "comment",
}

type jiraIssue struct {
	ID     string         `json:"id"`
	Key    string         `json:"key"`
	Fields map[string]any `json:"fields"`
}

type jiraSearchResponse struct {
	Issues        []jiraIssue `json:"issues"`
	NextPageToken string      `json:"nextPageToken"`
	IsLast        *bool       `json:"isLast"`
}

type parsedTicket struct {
	id          string
	summary     string
	description string
	ticketType  string
	status      string
	resolution  string
	createdAt   string
	updatedAt   string
	reporter    string
	assignee    string
	labels      []string
	comments    []ticketCommentOut
}

type ticketCommentOut struct {
	Author  string `json:"author"`
	Body    string `json:"body"`
	Created string `json:"created"`
}

type JiraSyncResult struct {
	Project     string
	TicketCount int
}

type JiraSyncOptions struct {
	Full bool
}

type jiraClient struct {
	httpClient *http.Client
	baseURL    string
}

func newJiraClient(ctx context.Context, dataDir string) (*jiraClient, error) {
	httpClient, cloudID, err := oauth.HTTPClient(ctx, dataDir)
	if err != nil {
		return nil, err
	}
	httpClient.Timeout = jiraTimeout
	return &jiraClient{
		httpClient: httpClient,
		baseURL:    strings.TrimRight(oauth.APIBaseURL, "/") + "/" + cloudID + "/rest/api/3",
	}, nil
}

func (c *jiraClient) doJSON(ctx context.Context, method, path string, body any, label string) ([]byte, error) {
	var payload io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("Jira API call failed (%s): marshal request: %w", label, err)
		}
		payload = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
	if err != nil {
		return nil, fmt.Errorf("Jira API call failed (%s): build request: %w", label, err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Jira API call failed (%s): %w", label, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("Jira API call failed (%s): read response: %w", label, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail := strings.TrimSpace(string(respBody))
		return nil, fmt.Errorf("Jira API call failed (%s): HTTP %d %s", label, resp.StatusCode, detail)
	}

	return respBody, nil
}

func SyncJiraProject(ctx context.Context, db *sql.DB, project config.Project, dataDir string, opts JiraSyncOptions) (JiraSyncResult, error) {
	client, err := newJiraClient(ctx, dataDir)
	if err != nil {
		return JiraSyncResult{}, err
	}

	var since string
	if !opts.Full {
		s, err := resolveIncrementalSince(db, project.Name)
		if err != nil {
			return JiraSyncResult{}, err
		}
		since = s
	}
	mode := "full"
	if since != "" {
		mode = "incremental since " + since + " UTC"
	}
	logger.Infof("[%s] fetching Jira issues for project %s (%s)", project.Name, project.JiraProjectKey, mode)

	issues, err := fetchAllIssues(ctx, client, project.JiraProjectKey, since)
	if err != nil {
		return JiraSyncResult{}, err
	}
	logger.Infof("[%s] fetched %d issues (comments inline)", project.Name, len(issues))

	truncated := 0
	tickets := make([]parsedTicket, 0, len(issues))
	for _, issue := range issues {
		comments, isTruncated := extractInlineComments(issue)
		if isTruncated {
			truncated++
		}
		tickets = append(tickets, toTicket(issue, comments))
	}

	if truncated > 0 {
		logger.Warnf(
			"[%s] %d ticket(s) had more comments than the inline page; only the first page is indexed",
			project.Name, truncated,
		)
	}

	upserted, err := upsertTickets(db, project.Name, tickets)
	if err != nil {
		return JiraSyncResult{}, err
	}
	if err := rebuildTicketsFTS(db); err != nil {
		return JiraSyncResult{}, err
	}
	total, err := countProjectTickets(db, project.Name)
	if err != nil {
		return JiraSyncResult{}, err
	}
	if err := updateJiraSyncState(db, project.Name, total); err != nil {
		return JiraSyncResult{}, err
	}

	logger.Infof("[%s] upserted %d ticket(s); %d total in database", project.Name, upserted, total)
	return JiraSyncResult{Project: project.Name, TicketCount: upserted}, nil
}

func fetchAllIssues(ctx context.Context, client *jiraClient, projectKey, since string) ([]jiraIssue, error) {
	clauses := []string{`project = "` + projectKey + `"`}
	if since != "" {
		clauses = append(clauses, `updated >= "`+since+`"`)
	}
	jql := strings.Join(clauses, " AND ") + " ORDER BY updated DESC"

	var issues []jiraIssue
	var nextPageToken string

	for {
		body := map[string]any{
			"jql":        jql,
			"fields":     searchFields,
			"maxResults": searchPageSize,
		}
		if nextPageToken != "" {
			body["nextPageToken"] = nextPageToken
		}

		respBody, err := client.doJSON(ctx, http.MethodPost, "/search/jql", body, "search issues")
		if err != nil {
			return nil, err
		}

		var page jiraSearchResponse
		if err := json.Unmarshal(respBody, &page); err != nil {
			return nil, fmt.Errorf("Jira API call failed (search issues): unmarshal: %w", err)
		}

		issues = append(issues, page.Issues...)
		if page.IsLast != nil && *page.IsLast {
			break
		}
		if page.NextPageToken == "" {
			break
		}
		nextPageToken = page.NextPageToken
	}

	return issues, nil
}

func resolveIncrementalSince(db *sql.DB, projectName string) (string, error) {
	lastSynced, ok, err := dbpkg.LastSynced(db, projectName, "jira")
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	return toJQLTimestamp(lastSynced, incrementalOverlapMinutes)
}

func toJQLTimestamp(iso string, overlapMinutes int) (string, error) {
	t, err := time.Parse(time.RFC3339Nano, iso)
	if err != nil {
		t, err = time.Parse(time.RFC3339, iso)
		if err != nil {
			return "", fmt.Errorf("Invalid last_synced timestamp: %s", iso)
		}
	}
	t = t.UTC().Add(-time.Duration(overlapMinutes) * time.Minute)
	return t.Format("2006-01-02 15:04"), nil
}

func extractInlineComments(issue jiraIssue) ([]map[string]any, bool) {
	field, ok := issue.Fields["comment"].(map[string]any)
	if !ok {
		return nil, false
	}

	rawComments, _ := field["comments"].([]any)
	comments := make([]map[string]any, 0, len(rawComments))
	for _, raw := range rawComments {
		if c, ok := raw.(map[string]any); ok {
			comments = append(comments, c)
		}
	}

	total := len(comments)
	if t, ok := field["total"].(float64); ok {
		total = int(t)
	}
	return comments, total > len(comments)
}

func toTicket(issue jiraIssue, comments []map[string]any) parsedTicket {
	fields := issue.Fields

	out := parsedTicket{
		id:          issue.Key,
		summary:     stringField(fields, "summary"),
		description: adf.ToPlain(fields["description"]),
		ticketType:  nestedString(fields, "issuetype", "name"),
		status:      nestedString(fields, "status", "name"),
		resolution:  nestedString(fields, "resolution", "name"),
		createdAt:   stringField(fields, "created"),
		updatedAt:   stringField(fields, "updated"),
		reporter:    nestedString(fields, "reporter", "displayName"),
		assignee:    nestedString(fields, "assignee", "displayName"),
	}

	if rawLabels, ok := fields["labels"].([]any); ok {
		for _, raw := range rawLabels {
			if s, ok := raw.(string); ok {
				out.labels = append(out.labels, s)
			}
		}
	}

	for _, comment := range comments {
		author := ""
		if a, ok := comment["author"].(map[string]any); ok {
			if name, ok := a["displayName"].(string); ok {
				author = name
			} else if email, ok := a["emailAddress"].(string); ok {
				author = email
			}
		}
		created := ""
		if c, ok := comment["created"].(string); ok {
			created = c
		}
		out.comments = append(out.comments, ticketCommentOut{
			Author:  author,
			Body:    adf.ToPlain(comment["body"]),
			Created: created,
		})
	}

	return out
}

func stringField(fields map[string]any, key string) string {
	if v, ok := fields[key].(string); ok {
		return v
	}
	return ""
}

func nestedString(fields map[string]any, key, nested string) string {
	v, ok := fields[key].(map[string]any)
	if !ok {
		return ""
	}
	if s, ok := v[nested].(string); ok {
		return s
	}
	return ""
}

func upsertTickets(db *sql.DB, projectName string, tickets []parsedTicket) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin ticket upsert: %w", err)
	}

	stmt, err := tx.Prepare(`
		INSERT INTO tickets (
		  id, project, summary, description, type, status, resolution,
		  created_at, updated_at, reporter, assignee, labels, comments
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		  project     = excluded.project,
		  summary     = excluded.summary,
		  description = excluded.description,
		  type        = excluded.type,
		  status      = excluded.status,
		  resolution  = excluded.resolution,
		  created_at  = excluded.created_at,
		  updated_at  = excluded.updated_at,
		  reporter    = excluded.reporter,
		  assignee    = excluded.assignee,
		  labels      = excluded.labels,
		  comments    = excluded.comments
	`)
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("failed to prepare ticket upsert: %w", err)
	}
	defer stmt.Close()

	for _, t := range tickets {
		labelsJSON, err := json.Marshal(t.labels)
		if err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("failed to marshal ticket labels: %w", err)
		}
		commentsJSON, err := json.Marshal(t.comments)
		if err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("failed to marshal ticket comments: %w", err)
		}
		if _, err := stmt.Exec(
			t.id, projectName, t.summary, t.description, t.ticketType, t.status, t.resolution,
			t.createdAt, t.updatedAt, t.reporter, t.assignee, string(labelsJSON), string(commentsJSON),
		); err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("failed to upsert ticket %s: %w", t.id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit ticket upsert: %w", err)
	}
	return len(tickets), nil
}

func rebuildTicketsFTS(db *sql.DB) error {
	if _, err := db.Exec("INSERT INTO tickets_fts (tickets_fts) VALUES ('rebuild')"); err != nil {
		return fmt.Errorf("failed to rebuild tickets_fts: %w", err)
	}
	return nil
}

func countProjectTickets(db *sql.DB, projectName string) (int, error) {
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM tickets WHERE project = ?", projectName).Scan(&n); err != nil {
		return 0, fmt.Errorf("failed to count tickets for %s: %w", projectName, err)
	}
	return n, nil
}

func updateJiraSyncState(db *sql.DB, projectName string, ticketCount int) error {
	_, err := db.Exec(`
		INSERT INTO sync_state (project, source, last_synced, commit_count, ticket_count)
		VALUES (
		  ?, 'jira',
		  ?,
		  (SELECT commit_count FROM sync_state WHERE project = ? AND source = 'jira'),
		  ?
		)
		ON CONFLICT(project, source) DO UPDATE SET
		  last_synced  = excluded.last_synced,
		  ticket_count = excluded.ticket_count
	`, projectName, time.Now().UTC().Format(time.RFC3339Nano), projectName, ticketCount)
	if err != nil {
		return fmt.Errorf("failed to update jira sync_state for %s: %w", projectName, err)
	}
	return nil
}
