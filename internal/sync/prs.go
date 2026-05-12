package sync

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	dbpkg "scout/internal/db"
)

const (
	GitHubTokenFileName    = "github_token.json"
	BitbucketTokenFileName = "bitbucket_token.json"

	prSearchPageSize = 100
	prHTTPTimeout    = 30 * time.Second
)

var (
	ErrGitHubTokenNotFound    = errors.New("GitHub token not found. Run 'scout github-login' first.")
	ErrBitbucketCredsNotFound = errors.New("Bitbucket API token not found. Run 'scout bitbucket-login' first.")
)

// parsedPR is the normalized, provider-agnostic representation written
// to the DB. Both GitHub and Bitbucket sync funnel into this shape.
type parsedPR struct {
	id             string
	project        string
	provider       string
	number         int
	title          string
	body           string
	state          string
	author         string
	createdAt      string
	updatedAt      string
	mergedAt       string
	sourceBranch   string
	targetBranch   string
	url            string
	reviewComments []prReviewComment
}

type prReviewComment struct {
	Author      string `json:"author"`
	Body        string `json:"body"`
	SubmittedAt string `json:"submitted_at"`
}

type PRSyncResult struct {
	Project string
	PRCount int
}

type PRSyncOptions struct {
	Full bool
}

type gitHubTokenFile struct {
	Token string `json:"token"`
}

type bitbucketTokenFile struct {
	Email    string `json:"email"`
	APIToken string `json:"api_token"`
}

func GitHubTokenPath(dataDir string) string {
	return filepath.Join(dataDir, GitHubTokenFileName)
}

func BitbucketTokenPath(dataDir string) string {
	return filepath.Join(dataDir, BitbucketTokenFileName)
}

func LoadGitHubToken(dataDir string) (string, error) {
	path := GitHubTokenPath(dataDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrGitHubTokenNotFound
		}
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	var f gitHubTokenFile
	if err := json.Unmarshal(data, &f); err != nil {
		return "", fmt.Errorf("parse %s: %w", path, err)
	}
	if f.Token == "" {
		return "", fmt.Errorf("%s exists but token field is empty", path)
	}
	return f.Token, nil
}

func SaveGitHubToken(dataDir, token string) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("create dataDir %s: %w", dataDir, err)
	}
	data, err := json.MarshalIndent(gitHubTokenFile{Token: token}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal github token: %w", err)
	}
	return writeFileAtomic(GitHubTokenPath(dataDir), data)
}

func DeleteGitHubToken(dataDir string) error {
	path := GitHubTokenPath(dataDir)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

func LoadBitbucketCreds(dataDir string) (string, string, error) {
	path := BitbucketTokenPath(dataDir)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", ErrBitbucketCredsNotFound
		}
		return "", "", fmt.Errorf("read %s: %w", path, err)
	}
	var f bitbucketTokenFile
	if err := json.Unmarshal(data, &f); err != nil {
		return "", "", fmt.Errorf("parse %s: %w", path, err)
	}
	if f.Email == "" || f.APIToken == "" {
		return "", "", fmt.Errorf("%s exists but email or api_token is empty", path)
	}
	return f.Email, f.APIToken, nil
}

func SaveBitbucketCreds(dataDir, email, apiToken string) error {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("create dataDir %s: %w", dataDir, err)
	}
	data, err := json.MarshalIndent(bitbucketTokenFile{Email: email, APIToken: apiToken}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal bitbucket creds: %w", err)
	}
	return writeFileAtomic(BitbucketTokenPath(dataDir), data)
}

func DeleteBitbucketCreds(dataDir string) error {
	path := BitbucketTokenPath(dataDir)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

func upsertPRs(db *sql.DB, prs []parsedPR) (int, error) {
	if len(prs) == 0 {
		return 0, nil
	}
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin PR upsert: %w", err)
	}

	stmt, err := tx.Prepare(`
		INSERT INTO pull_requests (
		  id, project, provider, number, title, body, state, author,
		  created_at, updated_at, merged_at, source_branch, target_branch,
		  url, review_comments
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project, id) DO UPDATE SET
		  title           = excluded.title,
		  body            = excluded.body,
		  state           = excluded.state,
		  author          = excluded.author,
		  updated_at      = excluded.updated_at,
		  merged_at       = excluded.merged_at,
		  source_branch   = excluded.source_branch,
		  target_branch   = excluded.target_branch,
		  url             = excluded.url,
		  review_comments = excluded.review_comments
	`)
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("failed to prepare PR upsert: %w", err)
	}
	defer stmt.Close()

	for _, pr := range prs {
		reviewJSON, err := json.Marshal(pr.reviewComments)
		if err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("failed to marshal review comments for %s: %w", pr.id, err)
		}
		var bodyArg any
		if pr.body == "" {
			bodyArg = nil
		} else {
			bodyArg = pr.body
		}
		if _, err := stmt.Exec(
			pr.id, pr.project, pr.provider, pr.number, pr.title, bodyArg, pr.state, pr.author,
			pr.createdAt, pr.updatedAt, pr.mergedAt, pr.sourceBranch, pr.targetBranch,
			pr.url, string(reviewJSON),
		); err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("failed to upsert PR %s: %w", pr.id, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit PR upsert: %w", err)
	}
	return len(prs), nil
}

func rebuildPRsFTS(db *sql.DB) error {
	if _, err := db.Exec("INSERT INTO pull_requests_fts (pull_requests_fts) VALUES ('rebuild')"); err != nil {
		return fmt.Errorf("failed to rebuild pull_requests_fts: %w", err)
	}
	return nil
}

func countProjectPRs(db *sql.DB, projectName string) (int, error) {
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM pull_requests WHERE project = ?", projectName).Scan(&n); err != nil {
		return 0, fmt.Errorf("failed to count PRs for %s: %w", projectName, err)
	}
	return n, nil
}

func updatePRSyncState(db *sql.DB, projectName string, prCount int) error {
	_, err := db.Exec(`
		INSERT INTO sync_state (project, source, last_synced, commit_count, ticket_count, file_count, pr_count)
		VALUES (
		  ?, 'prs',
		  ?,
		  (SELECT commit_count FROM sync_state WHERE project = ? AND source = 'prs'),
		  (SELECT ticket_count FROM sync_state WHERE project = ? AND source = 'prs'),
		  (SELECT file_count   FROM sync_state WHERE project = ? AND source = 'prs'),
		  ?
		)
		ON CONFLICT(project, source) DO UPDATE SET
		  last_synced = excluded.last_synced,
		  pr_count    = excluded.pr_count
	`, projectName, time.Now().UTC().Format(time.RFC3339Nano), projectName, projectName, projectName, prCount)
	if err != nil {
		return fmt.Errorf("failed to update prs sync_state for %s: %w", projectName, err)
	}
	return nil
}

// resolveIncrementalPRSince returns the cutoff timestamp for incremental
// PR sync — the last successful sync minus a 10-minute overlap to
// absorb clock skew between scout and the provider. Returns ("", false,
// nil) on the first sync. The returned timestamp is RFC3339 UTC, which
// both GitHub (`since` semantics on `updated_at`) and Bitbucket
// (`q=updated_on >= ...`) understand when we reuse it.
func resolveIncrementalPRSince(db *sql.DB, projectName string) (string, bool, error) {
	lastSynced, ok, err := dbpkg.LastSynced(db, projectName, "prs")
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}
	t, err := time.Parse(time.RFC3339Nano, lastSynced)
	if err != nil {
		t, err = time.Parse(time.RFC3339, lastSynced)
		if err != nil {
			return "", false, fmt.Errorf("Invalid last_synced timestamp for PRs: %s", lastSynced)
		}
	}
	t = t.UTC().Add(-time.Duration(incrementalOverlapMinutes) * time.Minute)
	return t.Format(time.RFC3339), true, nil
}

