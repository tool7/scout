# scout — usage reference

A local CLI that queries an indexed SQLite knowledge base of Git commits, Jira tickets, and source code from configured projects. The query sub-commands are **fully offline** — only `sync` makes network calls (to your Git remotes and Jira). Schema: SQLite + FTS5; commits/tickets use a Porter stemmer, code uses a trigram tokenizer.

This document is the authoritative usage reference. Treat it as ground truth for both humans and AI agents.

---

## Sub-commands at a glance

| Sub-command | Purpose                                                  | Reads | Writes | Network |
| ----------- | -------------------------------------------------------- | ----- | ------ | ------- |
| `search`    | Broad full-text search across commits, tickets, and code | ✓     |        |         |
| `history`   | Chronological timeline (commits + tickets) for a topic   | ✓     |        |         |
| `related`   | Jira tickets similar to a bug/behaviour description      | ✓     |        |         |
| `sync`      | Refresh the local index from Git remotes + Jira          | ✓     | ✓      | ✓       |
| `status`    | Show last sync time and record counts per project/source | ✓     |        |         |
| `jira-login`  | Authenticate to Jira via OAuth 2.0 (3LO) in a browser  |       | ✓      | ✓       |
| `jira-logout` | Forget the locally stored Jira OAuth tokens            |       | ✓      |         |

Pick exactly **one** sub-command per invocation.

---

## `scout search <query>`

Broad full-text search across all indexed Git commits, Jira tickets, and source-code files. Results are ranked by BM25 across all sources and merged into one list. Each line is prefixed with its source: `[commit · …]`, `[jira · …]`, or `[code · …]`.

| Flag                     | Default | Description                                                          |
| ------------------------ | ------- | -------------------------------------------------------------------- |
| `-p, --project <name>`   | —       | Restrict to a single configured project (use the project's `name`)   |
| `-s, --source <source>`  | `all`   | One of: `git`, `jira`, `code`, `all`                                 |
| `-l, --limit <n>`        | `20`    | Max results, 1–50                                                    |

Notes:
- Code search uses a **trigram tokenizer**, so substring matches work without wildcards (e.g. `parseConfig` matches `parseConfigJson`). `[code · …]` hits include a short FTS5 `snippet()` excerpt with `«match»` markers.
- Commit and ticket search use Porter stemming + a `*` prefix wildcard, so `auth` matches `authentication`, `authorise`, etc.
- Use `--source code` when the user explicitly wants source files only; otherwise leave at `all` for breadth.

Examples:
```sh
scout search 'saved searches'
scout search 'parseConfig' --source code --limit 10
scout search '"citation engine" failure' --project Papers-WebApp
```

---

## `scout history <topic>`

Unified chronological timeline for a topic / feature / file (commits + tickets, **oldest first**), top-ranked events only. **Source-code files are intentionally excluded** — files don't carry per-event timestamps suitable for a timeline. Use `search --source code` for code questions.

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

Jira tickets most similar to a bug or behaviour description. **Jira-only** by design — does not search commits or code.

| Flag                    | Default | Description                                  |
| ----------------------- | ------- | -------------------------------------------- |
| `-p, --project <name>`  | —       | Restrict to one Jira project                 |
| `-s, --status <bucket>` | `all`   | One of: `open`, `resolved`, `all`            |
| `-l, --limit <n>`       | `10`    | Max results, 1–30                            |

Each result includes the ticket header (key, type, status, resolution, assignee, last-updated date), summary, truncated description, and the **3 most recent comments** (or all of them if there are fewer than 3).

Examples:
```sh
scout related 'PDF export drops annotations'
scout related 'login loop on Safari' --status open
```

Use when: the user reports a bug or asks whether an issue has been seen before.

---

## `scout sync [options]`

Refresh the local index. **Makes network calls.** `git` syncs are always full (cheap — local `git log`); `jira` syncs are incremental by default; `code` syncs are content-diffed against the previous tree.

| Flag                    | Default | Description                                                      |
| ----------------------- | ------- | ---------------------------------------------------------------- |
| `-p, --project <name>`  | —       | Only sync the named project                                      |
| `-s, --source <source>` | `all`   | One of: `git`, `jira`, `code`, `all`                             |
| `-f, --full`            | `false` | Force full Jira/code re-fetch instead of incremental (no-op for git) |

Examples:
```sh
scout sync                                  # everything
scout sync -p Papers-WebApp -s git          # one project, one source
scout sync -s code                          # only refresh code indexes
scout sync --full                           # force full re-fetch
```

What each source does (high level):
- **git** — `git fetch <remote>`, then `git log --all` extraction, upsert into `commits`, rebuild `commits_fts`.
- **jira** — paginated `POST /rest/api/3/search/jql` with inline comments; ADF flattened to plain text; upsert into `tickets`, rebuild `tickets_fts`. Incremental window with a 10-minute backoff against the last successful sync.
- **code** — resolves the configured ref (default = `git symbolic-ref --short refs/remotes/origin/HEAD`), `git ls-tree -r --long`, content-diffs against the indexed tree, fetches new/changed blobs via `git cat-file`, applies the exclude / denylist / 1 MiB / strict-UTF-8 filter ladder, upserts into `files` (FTS triggers fire automatically).

When `--source all`, `git fetch` is deduplicated across the git and code passes — at most one fetch per project per run.

---

## `scout status`

Show last sync time and record counts per project/source. No flags. Output is a tab-aligned table with columns `project`, `source`, `last synced`, `records`, plus a totals header.

Example output:
```
Database:  /Users/you/.scout/data/knowledge.db
Totals:    9555 commit(s), 1149 ticket(s)

project             source  last synced               records
------------------  ------  ------------------------  -------
Papers Web App      git     2026-04-27T12:48:42.859Z  8692
Papers Web App      jira    2026-04-27T12:48:43.944Z  1090
Papers Web App      code    2026-04-27T12:48:44.559Z  1039
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

   Required fields: `dataDir`, `jira.host`, `projects[].{name,gitPath,jiraProjectKey}`. See `scout.config.example.json` in the repo for the full shape.
2. **Git** on `PATH` (only needed for `sync`).
3. **Jira login** — run `scout jira-login` once. This stores OAuth tokens at `<dataDir>/oauth_tokens.json`; subsequent `scout sync` runs refresh them automatically.
4. **A populated `knowledge.db`** — run `scout sync` at least once after logging in.

---

## When to use which sub-command

| User intent                                                     | Sub-command                       |
| --------------------------------------------------------------- | --------------------------------- |
| "Find anything about X"                                         | `search`                          |
| "Where is X defined / used in the code?"                        | `search --source code`            |
| "Why does X work this way?" / "When was Y introduced?"          | `history`                         |
| "Has this bug been seen before?" / "Is there a ticket about Z?" | `related`                         |
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
