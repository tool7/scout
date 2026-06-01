# scout — usage reference

A local CLI that queries an indexed SQLite knowledge base of Git commits, Jira tickets, source code, and pull requests from configured projects. The query sub-commands are **fully offline** — only `sync` makes network calls (to your Git remotes, Jira, GitHub, and Bitbucket). Schema: SQLite + FTS5; commits, tickets, and PRs use a Porter stemmer, code uses a trigram tokenizer.

This document is the authoritative usage reference. Treat it as ground truth for both humans and AI agents.

---

## Sub-commands at a glance

| Sub-command         | Purpose                                                       | Reads | Writes | Network |
| ------------------- | ------------------------------------------------------------- | ----- | ------ | ------- |
| `search`            | Broad full-text search across commits, tickets, code, and PRs | ✓     |        |         |
| `history`           | Chronological timeline (commits + tickets + PRs) for a topic  | ✓     |        |         |
| `related`           | Jira tickets similar to a bug/behaviour description           | ✓     |        |         |
| `sync`              | Refresh the local index from Git remotes + Jira + GitHub/Bitbucket | ✓     | ✓      | ✓       |
| `status`            | Show last sync time and record counts per project/source      | ✓     |        |         |
| `jira-login`        | Authenticate to Jira via OAuth 2.0 (3LO) in a browser         |       | ✓      | ✓       |
| `jira-logout`       | Forget the locally stored Jira OAuth tokens                   |       | ✓      |         |
| `github-login`      | Authorize scout via GitHub OAuth Device Flow in a browser     |       | ✓      | ✓       |
| `github-logout`     | Forget the locally stored GitHub OAuth token                  |       | ✓      |         |
| `bitbucket-login`   | Store and validate an Atlassian email + Bitbucket API token   |       | ✓      | ✓       |
| `bitbucket-logout`  | Forget the locally stored Bitbucket API token                 |       | ✓      |         |

Pick exactly **one** sub-command per invocation.

---

## `scout search <query>`

Broad full-text search across all indexed Git commits, Jira tickets, source-code files, and pull requests. Results are ranked by BM25 across all sources and merged into one list. Each line is prefixed with its source: `[commit · …]`, `[jira · …]`, `[code · …]`, or `[pr · …]`.

| Flag                     | Default | Description                                                          |
| ------------------------ | ------- | -------------------------------------------------------------------- |
| `-p, --project <name>`   | —       | Restrict to a single configured project (use the project's `name`)   |
| `-s, --source <source>`  | `all`   | One of: `git`, `jira`, `code`, `prs`, `all`                          |
| `-l, --limit <n>`        | `20`    | Max results, 1–50                                                    |
| `-f, --full`             | `false` | Render Jira tickets in full (untruncated description, every comment). No effect on non-ticket result kinds. |

Notes:
- Code search uses a **trigram tokenizer**, so substring matches work without wildcards (e.g. `parseConfig` matches `parseConfigJson`). `[code · …]` hits include a short FTS5 `snippet()` excerpt with `«match»` markers.
- Commit and ticket search use Porter stemming + a `*` prefix wildcard, so `auth` matches `authentication`, `authorise`, etc.
- Use `--source code` when the user explicitly wants source files only; otherwise leave at `all` for breadth.

Examples:
```sh
scout search 'saved searches'
scout search 'parseConfig' --source code --limit 10
scout search '"citation engine" failure' --project Project-Name
```

---

## `scout history <topic>`

Unified chronological timeline for a topic / feature / file (commits + tickets + PRs, **oldest first**), top-ranked events only. **Source-code files are intentionally excluded** — files don't carry per-event timestamps suitable for a timeline. Use `search --source code` for code questions. PRs use `merged_at` when merged, otherwise `updated_at`, so closed/open PRs still appear in the timeline.

| Flag                    | Default | Description                                       |
| ----------------------- | ------- | ------------------------------------------------- |
| `-p, --project <name>`  | —       | Restrict to one project                           |
| `--since <iso-date>`    | —       | ISO 8601 lower bound (e.g. `2024-01-01`)          |
| `-l, --limit <n>`       | `30`    | Max results, 1–100                                |

Examples:
```sh
scout history 'PDF export'
scout history 'highlight panel' --since 2024-01-01 --limit 50
```

Use when: the user asks *"why does X work this way?"* or *"when was Y introduced?"*.

---

## `scout related <description>`

Jira tickets most similar to a bug or behaviour description. **Jira-only** by design — does not search commits or code. Refuses to run if Jira isn't configured (no `jira.host` in the config); use `scout search --source code` or `scout history` for non-Jira lookups instead.

| Flag                    | Default | Description                                  |
| ----------------------- | ------- | -------------------------------------------- |
| `-p, --project <name>`  | —       | Restrict to one Jira project                 |
| `-s, --status <bucket>` | `all`   | One of: `open`, `resolved`, `all`            |
| `-l, --limit <n>`       | `10`    | Max results, 1–30                            |
| `-f, --full`            | `false` | Print each ticket without truncation: full description, every comment (not just the last 3). |

Each result includes the ticket header (key, type, status, resolution, assignee, last-updated date), summary, truncated description, and the **3 most recent comments** (or all of them if there are fewer than 3). Pass `--full` to disable truncation and print every comment.

Examples:
```sh
scout related 'PDF export drops annotations'
scout related 'login loop on Safari' --status open
scout related 'rate limiting on uploads' --limit 1 --full   # read the top match in full
```

Use when: the user reports a bug or asks whether an issue has been seen before.

---

## `scout sync [options]`

Refresh the local index. **Makes network calls.** `git` syncs are always full (cheap — local `git log`); `jira` and `prs` syncs are incremental by default; `code` syncs are content-diffed against the previous tree. Jira sync is skipped automatically for any project without `jiraProjectKey`, and entirely if `jira.host` is unset; `--source jira` errors loudly in those cases. PR sync is skipped for any project without `githubRepo` / `bitbucketRepo` set; `--source prs` errors loudly when no project is configured for PRs.

| Flag                    | Default | Description                                                      |
| ----------------------- | ------- | ---------------------------------------------------------------- |
| `-p, --project <name>`  | —       | Only sync the named project                                      |
| `-s, --source <source>` | `all`   | One of: `git`, `jira`, `code`, `prs`, `all`                      |
| `-f, --full`            | `false` | Force full Jira/code/PR re-fetch instead of incremental (no-op for git) |

Examples:
```sh
scout sync                                  # everything
scout sync -p Project-Name -s git           # one project, one source
scout sync -s code                          # only refresh code indexes
scout sync --full                           # force full re-fetch
```

What each source does (high level):
- **git** — `git fetch <remote>`, then `git log --all` extraction, upsert into `commits`, rebuild `commits_fts`.
- **jira** — paginated `POST /rest/api/3/search/jql` with inline comments; ADF flattened to plain text; upsert into `tickets`, rebuild `tickets_fts`. Incremental window with a 10-minute backoff against the last successful sync.
- **code** — resolves the configured ref (default = `git symbolic-ref --short refs/remotes/origin/HEAD`), `git ls-tree -r --long`, content-diffs against the indexed tree, fetches new/changed blobs via `git cat-file`, applies the exclude / denylist / 1 MiB / strict-UTF-8 filter ladder, upserts into `files` (FTS triggers fire automatically).
- **prs** — for each project with `githubRepo` set, paginated `GET /repos/{owner}/{repo}/pulls?state=all&sort=updated&direction=desc` plus per-PR `GET /repos/{owner}/{repo}/pulls/{n}/reviews`. For each project with `bitbucketRepo` + `bitbucketWorkspace` set, paginated `GET /repositories/{workspace}/{repo}/pullrequests` plus per-PR `GET …/comments`. Both providers normalize state to `open` / `merged` / `closed`, upsert into `pull_requests`, and rebuild `pull_requests_fts`. Same 10-minute incremental overlap as Jira.

When `--source all`, `git fetch` is deduplicated across the git and code passes — at most one fetch per project per run.

---

## `scout status`

Show last sync time and record counts per project/source. No flags. Output is a tab-aligned table with columns `project`, `source`, `last synced`, `records`, plus a totals header.

Example output:
```
Database:  /Users/you/.scout/data/knowledge.db
Totals:    9555 commit(s), 1149 ticket(s), 312 PR(s)

project             source  last synced               records
------------------  ------  ------------------------  -------
Project-Name        git     2026-04-27T12:48:42.859Z  8692
Project-Name        jira    2026-04-27T12:48:43.944Z  1090
Project-Name        code    2026-04-27T12:48:44.559Z  1039
Project-Name        prs     2026-04-27T12:48:45.211Z  312
```

Use when: confirming the index is fresh before relying on a query.

---

## Output and exit codes

- **stdout** — query results, formatted plain text. Relay verbatim.
- **stderr** — log lines in the format `[<ISO-8601 UTC>] <LEVEL> <message>` (levels: `INFO`, `WARN`, `ERROR`), plus error messages on failure.
- **Exit 0** — success, including the case where the query matched nothing (the CLI prints `No matches for "<query>".` to stdout).
- **Exit 1** — any error: missing config, invalid flag, validation failure, empty query (`No usable search terms in "<input>".`), Git/Jira failure, etc.

---

## Prerequisites

1. **Config file** at one of:
   - `<cwd-or-ancestor>/scout.config.json`
   - `<cwd-or-ancestor>/.scout.json`
   - `<cwd-or-ancestor>/.config/scout/config.json`
   - `~/.scout/config.json` (fallback)

   Required fields: `dataDir`, `projects[].{name,gitPath}`. Optional: the entire `jira` block (with `jira.host`) and `projects[].jiraProjectKey` — set both to enable Jira indexing, omit both for a Git/code-only setup. PR indexing is optional too: set `projects[].githubRepo` (`owner/repo`) for GitHub, or `projects[].bitbucketRepo` + `bitbucketWorkspace` for Bitbucket. See `scout.config.example.json` in the repo for the full shape.
2. **Git** on `PATH` (only needed for `sync`).
3. **Jira login** — only required if Jira is configured. Run `scout jira-login` once; OAuth tokens land at `<dataDir>/jira_tokens.json` and refresh automatically on subsequent `scout sync` runs.
4. **GitHub / Bitbucket login** — required for `--source prs` when the corresponding provider is configured. Run `scout github-login` and/or `scout bitbucket-login` once; tokens land at `<dataDir>/github_token.json` / `<dataDir>/bitbucket_token.json` (`0600` permissions).
5. **A populated `knowledge.db`** — run `scout sync` at least once.

---

## When to use which sub-command

| User intent                                                     | Sub-command                       |
| --------------------------------------------------------------- | --------------------------------- |
| "Find anything about X"                                         | `search`                          |
| "Where is X defined / used in the code?"                        | `search --source code`            |
| "Why does X work this way?" / "When was Y introduced?"          | `history`                         |
| "Has this bug been seen before?" / "Is there a ticket about Z?" | `related`                         |
| "What did the review discussion say about X?"                   | `search --source prs`             |
| "Is the index fresh?" / "When was the last sync?"               | `status`                          |
| "Refresh the index" (only on explicit user request)             | `sync`                            |

---

## Notes for AI agents

- Quote the free-text argument with **single quotes** to protect shell metacharacters; escape embedded single quotes by closing, inserting `\'`, and reopening.
- Treat the CLI's stdout as the **source of truth**. Do not paraphrase, re-rank, omit, or summarise results unless the user asks.
- On non-zero exit, surface stderr verbatim. Do not retry with a different query unless the user asks.
- `No matches for "..."` is **exit 0** — treat as a normal empty result, not an error.
- Do **not** read the SQLite database directly, shell out to `sqlite3`, or bypass this CLI. All reads go through `scout`.
- Do **not** run `scout sync` unless the user explicitly asks. Stale data is the user's call to refresh.
- Do **not** edit the config file or `knowledge.db`. Direct the user to do it manually.
