package sync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/readcube/readcube-scout/internal/config"
	"github.com/readcube/readcube-scout/internal/logger"
)

const maxFileBytes = 1 * 1024 * 1024

var extensionDenylist = map[string]struct{}{
	".lock":    {},
	".map":     {},
	".snap":    {},
	".min.js":  {},
	".min.css": {},
}

var filenameDenylist = map[string]struct{}{
	"package-lock.json": {},
	"yarn.lock":         {},
	"pnpm-lock.yaml":    {},
	"Gemfile.lock":      {},
	"go.sum":            {},
	"poetry.lock":       {},
	"Cargo.lock":        {},
}

var extensionLanguage = map[string]string{
	".ts":    "ts",
	".tsx":   "tsx",
	".js":    "js",
	".jsx":   "jsx",
	".mjs":   "js",
	".cjs":   "js",
	".rb":    "ruby",
	".go":    "go",
	".py":    "python",
	".rs":    "rust",
	".java":  "java",
	".kt":    "kotlin",
	".swift": "swift",
	".c":     "c",
	".h":     "c",
	".cpp":   "cpp",
	".hpp":   "cpp",
	".cs":    "csharp",
	".sql":   "sql",
	".sh":    "shell",
	".bash":  "shell",
	".zsh":   "shell",
	".md":    "markdown",
	".json":  "json",
	".yml":   "yaml",
	".yaml":  "yaml",
	".toml":  "toml",
	".html":  "html",
	".css":   "css",
	".scss":  "scss",
}

var blobHashPattern = regexp.MustCompile(`^[0-9a-f]{40}$|^[0-9a-f]{64}$`)

type treeEntry struct {
	blobHash  string
	sizeBytes int64
	path      string
}

type pendingUpsert struct {
	project   string
	path      string
	blobHash  string
	sizeBytes int64
	language  string
	content   string
	indexedAt string
}

type CodeSyncResult struct {
	Project   string
	FileCount int
}

type CodeSyncOptions struct {
	Full bool
}

func SyncCodeProject(ctx context.Context, db *sql.DB, project config.Project, fetched FetchSet, opts CodeSyncOptions) (CodeSyncResult, error) {
	if err := GitFetchOnce(ctx, project, fetched); err != nil {
		return CodeSyncResult{}, err
	}

	ref, err := resolveIndexRef(ctx, project)
	if err != nil {
		return CodeSyncResult{}, err
	}

	if opts.Full {
		if _, err := db.Exec("DELETE FROM files WHERE project = ?", project.Name); err != nil {
			return CodeSyncResult{}, fmt.Errorf("failed to clear files for %s: %w", project.Name, err)
		}
	}

	logger.Infof("[%s] reading tree at %s", project.Name, ref)
	tree, err := readTree(ctx, project.GitPath, ref)
	if err != nil {
		return CodeSyncResult{}, err
	}

	indexed, err := readIndexedHashes(db, project.Name)
	if err != nil {
		return CodeSyncResult{}, err
	}
	isExcluded := makeExcludeMatcher(project.ExcludePaths)

	inserted := 0
	updated := 0
	skipped := 0

	// Phase 1 (async): walk the tree and decide what to write. We can't put
	// `git cat-file` inside a single transaction because the child process can
	// stall — accumulate intents and flush them atomically in Phase 2.
	var pendingUpserts []pendingUpsert

	for _, entry := range tree {
		if existingHash, ok := indexed[entry.path]; ok && existingHash == entry.blobHash {
			delete(indexed, entry.path)
			continue
		}

		if preFilter(entry, isExcluded) {
			// If a previously-indexed file now matches an exclude or denylist
			// rule, we want it gone from the index. The path stays in `indexed`
			// here so the post-loop sweep removes it from the DB.
			skipped++
			continue
		}

		if !blobHashPattern.MatchString(entry.blobHash) {
			logger.Warnf("[%s] skipping %s: invalid blob hash %q", project.Name, entry.path, entry.blobHash)
			skipped++
			continue
		}

		bytes, err := fetchBlob(ctx, project.GitPath, entry.blobHash)
		if err != nil {
			logger.Warnf("[%s] failed to fetch blob %s for %s: %s", project.Name, entry.blobHash, entry.path, err)
			skipped++
			continue
		}

		if !utf8.Valid(bytes) {
			skipped++
			continue
		}

		_, isUpdate := indexed[entry.path]
		pendingUpserts = append(pendingUpserts, pendingUpsert{
			project:   project.Name,
			path:      entry.path,
			blobHash:  entry.blobHash,
			sizeBytes: entry.sizeBytes,
			language:  detectLanguage(entry.path),
			content:   string(bytes),
			indexedAt: time.Now().UTC().Format(time.RFC3339Nano),
		})

		if isUpdate {
			updated++
		} else {
			inserted++
		}
		delete(indexed, entry.path)
	}

	// Anything still in `indexed` was either absent from the current tree, or
	// present in the tree but skipped this round (newly excluded, too big,
	// failed to fetch, not UTF-8). Schedule them for removal so the index
	// reflects what should currently be searchable.
	pendingDeletes := make([]string, 0, len(indexed))
	for p := range indexed {
		pendingDeletes = append(pendingDeletes, p)
	}
	deleted := len(pendingDeletes)

	if err := flushCodeWrites(db, project.Name, pendingUpserts, pendingDeletes); err != nil {
		return CodeSyncResult{}, err
	}

	var total int
	if err := db.QueryRow("SELECT COUNT(*) FROM files WHERE project = ?", project.Name).Scan(&total); err != nil {
		return CodeSyncResult{}, fmt.Errorf("failed to count files for %s: %w", project.Name, err)
	}

	if err := updateCodeSyncState(db, project.Name, total); err != nil {
		return CodeSyncResult{}, err
	}

	logger.Infof(
		"[%s] code sync: +%d new, ~%d updated, -%d removed, %d skipped; %d total file(s) in database",
		project.Name, inserted, updated, deleted, skipped, total,
	)

	return CodeSyncResult{Project: project.Name, FileCount: total}, nil
}

func flushCodeWrites(db *sql.DB, projectName string, upserts []pendingUpsert, deletes []string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin code flush: %w", err)
	}

	upsertStmt, err := tx.Prepare(`
		INSERT INTO files (project, path, blob_hash, size_bytes, language, content, indexed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(project, path) DO UPDATE SET
		  blob_hash  = excluded.blob_hash,
		  size_bytes = excluded.size_bytes,
		  language   = excluded.language,
		  content    = excluded.content,
		  indexed_at = excluded.indexed_at
	`)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to prepare file upsert: %w", err)
	}
	defer upsertStmt.Close()

	deleteStmt, err := tx.Prepare("DELETE FROM files WHERE project = ? AND path = ?")
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to prepare file delete: %w", err)
	}
	defer deleteStmt.Close()

	for _, u := range upserts {
		var lang any
		if u.language != "" {
			lang = u.language
		}
		if _, err := upsertStmt.Exec(u.project, u.path, u.blobHash, u.sizeBytes, lang, u.content, u.indexedAt); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to upsert file %s: %w", u.path, err)
		}
	}
	for _, p := range deletes {
		if _, err := deleteStmt.Exec(projectName, p); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to delete file %s: %w", p, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit code flush: %w", err)
	}
	return nil
}

func resolveIndexRef(ctx context.Context, project config.Project) (string, error) {
	if project.IndexRef != "" {
		return project.IndexRef, nil
	}
	cmd := exec.CommandContext(ctx, "git", "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	cmd.Dir = project.GitPath
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf(
			`Could not resolve default branch for %q — set indexRef explicitly in config`,
			project.Name,
		)
	}
	ref := strings.TrimSpace(string(out))
	if ref == "" {
		return "", fmt.Errorf(
			`Could not resolve default branch for %q — set indexRef explicitly in config`,
			project.Name,
		)
	}
	return ref, nil
}

func readTree(ctx context.Context, gitPath, ref string) ([]treeEntry, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-tree", "-r", "--long", ref)
	cmd.Dir = gitPath
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			stderr = strings.TrimSpace(string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("git ls-tree failed in %s: %s: %w", gitPath, stderr, err)
	}

	var entries []treeEntry
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		tabIndex := strings.IndexByte(line, '\t')
		if tabIndex < 0 {
			continue
		}
		meta := line[:tabIndex]
		filePath := line[tabIndex+1:]

		fields := strings.Fields(meta)
		if len(fields) < 4 {
			continue
		}
		objectType := fields[1]
		blobHash := fields[2]
		sizeStr := fields[3]
		if objectType != "blob" {
			continue
		}
		size, err := strconv.ParseInt(sizeStr, 10, 64)
		if err != nil {
			continue
		}
		entries = append(entries, treeEntry{
			blobHash:  blobHash,
			sizeBytes: size,
			path:      filePath,
		})
	}

	return entries, nil
}

func readIndexedHashes(db *sql.DB, projectName string) (map[string]string, error) {
	rows, err := db.Query("SELECT path, blob_hash FROM files WHERE project = ?", projectName)
	if err != nil {
		return nil, fmt.Errorf("failed to read indexed files for %s: %w", projectName, err)
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var p, h string
		if err := rows.Scan(&p, &h); err != nil {
			return nil, fmt.Errorf("failed to scan indexed file row: %w", err)
		}
		out[p] = h
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate indexed files: %w", err)
	}
	return out, nil
}

func makeExcludeMatcher(patterns []string) func(string) bool {
	if len(patterns) == 0 {
		return func(string) bool { return false }
	}
	return func(p string) bool {
		for _, pattern := range patterns {
			if ok, err := doublestar.PathMatch(pattern, p); err == nil && ok {
				return true
			}
		}
		return false
	}
}

func preFilter(entry treeEntry, isExcluded func(string) bool) bool {
	if isExcluded(entry.path) {
		return true
	}
	if entry.sizeBytes > maxFileBytes {
		return true
	}
	if matchesDenylist(entry.path) {
		return true
	}
	return false
}

func matchesDenylist(p string) bool {
	filename := p
	if i := strings.LastIndex(p, "/"); i >= 0 {
		filename = p[i+1:]
	}
	if _, ok := filenameDenylist[filename]; ok {
		return true
	}
	lower := strings.ToLower(filename)
	for ext := range extensionDenylist {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func detectLanguage(p string) string {
	ext := strings.ToLower(path.Ext(p))
	return extensionLanguage[ext]
}

func fetchBlob(ctx context.Context, cwd, blobHash string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", "cat-file", "-p", blobHash)
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			stderr = strings.TrimSpace(string(exitErr.Stderr))
		}
		if stderr != "" {
			return nil, fmt.Errorf("%s: %w", stderr, err)
		}
		return nil, err
	}
	return out, nil
}

func updateCodeSyncState(db *sql.DB, projectName string, fileCount int) error {
	_, err := db.Exec(`
		INSERT INTO sync_state (project, source, last_synced, commit_count, ticket_count, file_count)
		VALUES (?, 'code', ?, NULL, NULL, ?)
		ON CONFLICT(project, source) DO UPDATE SET
		  last_synced = excluded.last_synced,
		  file_count  = excluded.file_count
	`, projectName, time.Now().UTC().Format(time.RFC3339Nano), fileCount)
	if err != nil {
		return fmt.Errorf("failed to update code sync_state for %s: %w", projectName, err)
	}
	return nil
}
