package format

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/readcube/readcube-scout/internal/db"
)

const (
	maxBodyChars        = 400
	maxDescriptionChars = 400
	maxCommentChars     = 200
	maxFilesShown       = 8
	maxCommentsShown    = 3
)

type ticketComment struct {
	Author  string `json:"author"`
	Body    string `json:"body"`
	Created string `json:"created"`
}

var whitespaceRun = regexp.MustCompile(`\s+`)

func truncate(text string, max int) string {
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return strings.TrimRight(string(runes[:max]), " \t\n\r") + "…"
}

func shortHash(sha string) string {
	if len(sha) <= 8 {
		return sha
	}
	return sha[:8]
}

func isoDay(timestamp string) string {
	if len(timestamp) < 10 {
		return ""
	}
	return timestamp[:10]
}

func parseStringArray(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func parseCommentArray(raw string) []ticketComment {
	if raw == "" {
		return nil
	}
	var out []ticketComment
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func joinNonEmpty(parts []string, sep string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}

func Commit(commit db.CommitRow) string {
	header := "[commit · " + commit.Project + "] " +
		shortHash(commit.ID) + " · " + isoDay(commit.Date) + " · " + commit.Author

	body := strings.TrimSpace(commit.Body)
	bodyLine := ""
	if body != "" {
		bodyLine = "\n" + truncate(body, maxBodyChars)
	}

	files := parseStringArray(commit.Files)
	filesLine := ""
	if len(files) > 0 {
		shown := files
		more := ""
		if len(files) > maxFilesShown {
			shown = files[:maxFilesShown]
			more = " (+" + strconv.Itoa(len(files)-maxFilesShown) + " more)"
		}
		filesLine = "\nFiles: " + strings.Join(shown, ", ") + more
	}

	return header + "\n" + commit.Message + bodyLine + filesLine
}

func Ticket(ticket db.TicketRow, includeComments bool) string {
	typeStatus := joinNonEmpty([]string{ticket.Type, ticket.Status}, " · ")

	resolution := ""
	if ticket.Resolution != "" {
		resolution = " · resolved: " + ticket.Resolution
	}

	assignee := ""
	if ticket.Assignee != "" {
		assignee = " · assignee: " + ticket.Assignee
	}

	updated := ""
	if ticket.UpdatedAt != "" {
		updated = " · updated " + isoDay(ticket.UpdatedAt)
	}

	header := "[jira · " + ticket.Project + "] " + ticket.ID
	if typeStatus != "" {
		header += " · " + typeStatus
	}
	header += resolution + assignee + updated

	summary := "Summary: " + ticket.Summary

	description := strings.TrimSpace(ticket.Description)
	descriptionLine := ""
	if description != "" {
		descriptionLine = "\n" + truncate(description, maxDescriptionChars)
	}

	if !includeComments {
		return header + "\n" + summary + descriptionLine
	}

	comments := parseCommentArray(ticket.Comments)
	if len(comments) == 0 {
		return header + "\n" + summary + descriptionLine
	}

	start := len(comments) - maxCommentsShown
	if start < 0 {
		start = 0
	}
	latest := comments[start:]

	commentLines := make([]string, 0, len(latest))
	for _, c := range latest {
		body := whitespaceRun.ReplaceAllString(c.Body, " ")
		commentLines = append(commentLines, "  - "+c.Author+" ("+isoDay(c.Created)+"): "+truncate(body, maxCommentChars))
	}

	commentsHeader := "Comments:"
	if len(comments) > maxCommentsShown {
		commentsHeader = "Recent comments (last " + strconv.Itoa(maxCommentsShown) + " of " + strconv.Itoa(len(comments)) + "):"
	}

	return header + "\n" + summary + descriptionLine + "\n" + commentsHeader + "\n" + strings.Join(commentLines, "\n")
}

func File(file db.FileRow) string {
	lang := ""
	if file.Language != "" {
		lang = " · " + file.Language
	}
	header := "[code · " + file.Project + lang + "] " + file.Path
	return header + "\n" + strings.TrimSpace(file.Snippet)
}
