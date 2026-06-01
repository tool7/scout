package format

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"scout/internal/db"
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

// TicketDetail controls how much of a ticket the formatter prints.
// The three levels exist because the three callers want measurably
// different verbosity:
//   - Compact:  search / history results — one-liner-ish, no comments,
//     description truncated. Keeps mixed-source result lists scannable.
//   - Standard: related results — last 3 comments, description and
//     each comment truncated. Enough to triage but not overwhelming.
//   - Full:     --full flag — no truncation anywhere, every comment.
//     For "I want to read this ticket end to end" reading mode.
type TicketDetail int

const (
	TicketCompact TicketDetail = iota
	TicketStandard
	TicketFull
)

func Ticket(ticket db.TicketRow, detail TicketDetail) string {
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
		if detail == TicketFull {
			descriptionLine = "\n" + description
		} else {
			descriptionLine = "\n" + truncate(description, maxDescriptionChars)
		}
	}

	if detail == TicketCompact {
		return header + "\n" + summary + descriptionLine
	}

	comments := parseCommentArray(ticket.Comments)
	if len(comments) == 0 {
		return header + "\n" + summary + descriptionLine
	}

	var shown []ticketComment
	var commentsHeader string
	if detail == TicketFull {
		shown = comments
		commentsHeader = "Comments (" + strconv.Itoa(len(comments)) + "):"
	} else {
		start := len(comments) - maxCommentsShown
		if start < 0 {
			start = 0
		}
		shown = comments[start:]
		if len(comments) > maxCommentsShown {
			commentsHeader = "Recent comments (last " + strconv.Itoa(maxCommentsShown) + " of " + strconv.Itoa(len(comments)) + "):"
		} else {
			commentsHeader = "Comments:"
		}
	}

	commentLines := make([]string, 0, len(shown))
	for _, c := range shown {
		body := whitespaceRun.ReplaceAllString(c.Body, " ")
		if detail != TicketFull {
			body = truncate(body, maxCommentChars)
		}
		commentLines = append(commentLines, "  - "+c.Author+" ("+isoDay(c.Created)+"): "+body)
	}

	return header + "\n" + summary + descriptionLine + "\n" + commentsHeader + "\n" + strings.Join(commentLines, "\n")
}

type prReviewComment struct {
	Author      string `json:"author"`
	Body        string `json:"body"`
	SubmittedAt string `json:"submitted_at"`
}

func parsePRReviewArray(raw string) []prReviewComment {
	if raw == "" {
		return nil
	}
	var out []prReviewComment
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func PR(pr db.PRRow) string {
	updated := ""
	if pr.UpdatedAt != "" {
		updated = " · updated " + isoDay(pr.UpdatedAt)
	}
	state := pr.State
	if state == "" {
		state = "unknown"
	}
	author := ""
	if pr.Author != "" {
		author = " · " + pr.Author
	}

	header := "[pr · " + pr.Project + "] #" + strconv.Itoa(pr.Number) +
		" · " + state + author + updated

	title := "Title: " + pr.Title

	body := strings.TrimSpace(pr.Body)
	bodyLine := ""
	if body != "" {
		bodyLine = "\n" + truncate(body, maxBodyChars)
	}

	reviews := parsePRReviewArray(pr.ReviewComments)
	if len(reviews) == 0 {
		return header + "\n" + title + bodyLine
	}

	start := len(reviews) - maxCommentsShown
	if start < 0 {
		start = 0
	}
	latest := reviews[start:]

	lines := make([]string, 0, len(latest))
	for _, c := range latest {
		text := whitespaceRun.ReplaceAllString(c.Body, " ")
		lines = append(lines, "  - "+c.Author+" ("+isoDay(c.SubmittedAt)+"): "+truncate(text, maxCommentChars))
	}

	commentsHeader := "Review comments:"
	if len(reviews) > maxCommentsShown {
		commentsHeader = "Review comments (last " + strconv.Itoa(maxCommentsShown) + " of " + strconv.Itoa(len(reviews)) + "):"
	}

	return header + "\n" + title + bodyLine + "\n" + commentsHeader + "\n" + strings.Join(lines, "\n")
}

func File(file db.FileRow) string {
	lang := ""
	if file.Language != "" {
		lang = " · " + file.Language
	}
	header := "[code · " + file.Project + lang + "] " + file.Path
	return header + "\n" + strings.TrimSpace(file.Snippet)
}
