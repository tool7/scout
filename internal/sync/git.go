package sync

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/readcube/readcube-scout/internal/config"
	"github.com/readcube/readcube-scout/internal/logger"
)

const (
	recordSeparator = "\x1e"
	unitSeparator   = "\x1f"
	logFormat       = recordSeparator + "%H" + unitSeparator + "%an" + unitSeparator + "%aI" + unitSeparator + "%s" + unitSeparator + "%b" + unitSeparator
)

type parsedCommit struct {
	hash    string
	author  string
	date    string
	subject string
	body    string
	files   []string
}

type GitSyncResult struct {
	Project     string
	CommitCount int
}

// FetchSet tracks which (project, remote) pairs we've already fetched in this
// sync run so commit-sync and code-sync can share a single network roundtrip
// when --source=all.
type FetchSet map[string]struct{}

func NewFetchSet() FetchSet { return make(FetchSet) }

func GitFetchOnce(ctx context.Context, project config.Project, fetched FetchSet) error {
	if _, err := os.Stat(project.GitPath); err != nil {
		return fmt.Errorf(
			`gitPath %q does not exist for project %q`,
			project.GitPath, project.Name,
		)
	}
	key := project.Name + "::" + project.GitRemote
	if _, ok := fetched[key]; ok {
		return nil
	}

	logger.Infof("[%s] fetching %s", project.Name, project.GitRemote)
	cmd := exec.CommandContext(ctx, "git", "fetch", project.GitRemote)
	cmd.Dir = project.GitPath
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch failed for project %q: %s: %w", project.Name, strings.TrimSpace(string(out)), err)
	}
	fetched[key] = struct{}{}
	return nil
}

func SyncGitProject(ctx context.Context, db *sql.DB, project config.Project, fetched FetchSet) (GitSyncResult, error) {
	if err := GitFetchOnce(ctx, project, fetched); err != nil {
		return GitSyncResult{}, err
	}

	logger.Infof("[%s] reading commit history", project.Name)
	commits, err := readCommits(ctx, project.GitPath)
	if err != nil {
		return GitSyncResult{}, err
	}

	upserted, err := upsertCommits(db, project.Name, commits)
	if err != nil {
		return GitSyncResult{}, err
	}
	if err := rebuildCommitsFTS(db); err != nil {
		return GitSyncResult{}, err
	}
	total, err := countProjectCommits(db, project.Name)
	if err != nil {
		return GitSyncResult{}, err
	}
	if err := updateGitSyncState(db, project.Name, total); err != nil {
		return GitSyncResult{}, err
	}

	logger.Infof("[%s] upserted %d commit(s); %d total in database", project.Name, upserted, total)
	return GitSyncResult{Project: project.Name, CommitCount: upserted}, nil
}

func readCommits(ctx context.Context, gitPath string) ([]parsedCommit, error) {
	cmd := exec.CommandContext(ctx, "git", "log", "--all", "--pretty=format:"+logFormat, "--name-only")
	cmd.Dir = gitPath
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr = strings.TrimSpace(string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("git log failed in %s: %s: %w", gitPath, stderr, err)
	}

	var commits []parsedCommit
	for _, chunk := range strings.Split(string(out), recordSeparator) {
		if chunk == "" {
			continue
		}
		commit, ok := parseRecord(chunk)
		if !ok {
			continue
		}
		commits = append(commits, commit)
	}
	return commits, nil
}

func parseRecord(record string) (parsedCommit, bool) {
	parts := strings.SplitN(record, unitSeparator, 6)
	if len(parts) < 6 {
		return parsedCommit{}, false
	}
	hash, author, date, subject, body, filesSection := parts[0], parts[1], parts[2], parts[3], parts[4], parts[5]
	if hash == "" || author == "" || date == "" {
		return parsedCommit{}, false
	}

	var files []string
	for _, line := range strings.Split(filesSection, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}

	return parsedCommit{
		hash:    hash,
		author:  author,
		date:    date,
		subject: subject,
		body:    body,
		files:   files,
	}, true
}

func upsertCommits(db *sql.DB, projectName string, commits []parsedCommit) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin commit upsert: %w", err)
	}

	stmt, err := tx.Prepare(`
		INSERT INTO commits (id, project, author, date, message, body, files)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		  project = excluded.project,
		  author  = excluded.author,
		  date    = excluded.date,
		  message = excluded.message,
		  body    = excluded.body,
		  files   = excluded.files
	`)
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("failed to prepare commit upsert: %w", err)
	}
	defer stmt.Close()

	for _, c := range commits {
		filesJSON, err := json.Marshal(c.files)
		if err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("failed to marshal commit files: %w", err)
		}
		if _, err := stmt.Exec(c.hash, projectName, c.author, c.date, c.subject, c.body, string(filesJSON)); err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("failed to upsert commit %s: %w", c.hash, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit commit upsert: %w", err)
	}
	return len(commits), nil
}

func rebuildCommitsFTS(db *sql.DB) error {
	if _, err := db.Exec("INSERT INTO commits_fts (commits_fts) VALUES ('rebuild')"); err != nil {
		return fmt.Errorf("failed to rebuild commits_fts: %w", err)
	}
	return nil
}

func countProjectCommits(db *sql.DB, projectName string) (int, error) {
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM commits WHERE project = ?", projectName).Scan(&n); err != nil {
		return 0, fmt.Errorf("failed to count commits for %s: %w", projectName, err)
	}
	return n, nil
}

func updateGitSyncState(db *sql.DB, projectName string, commitCount int) error {
	_, err := db.Exec(`
		INSERT INTO sync_state (project, source, last_synced, commit_count, ticket_count)
		VALUES (
		  ?, 'git', ?, ?,
		  (SELECT ticket_count FROM sync_state WHERE project = ? AND source = 'git')
		)
		ON CONFLICT(project, source) DO UPDATE SET
		  last_synced  = excluded.last_synced,
		  commit_count = excluded.commit_count
	`, projectName, time.Now().UTC().Format(time.RFC3339Nano), commitCount, projectName)
	if err != nil {
		return fmt.Errorf("failed to update git sync_state for %s: %w", projectName, err)
	}
	return nil
}
