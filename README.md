# readcube-scout

A local CLI that gives ReadCube developers and QA engineers a conversational interface into the team's collective domain knowledge. It indexes **Git history** and **Jira tickets** across a configurable set of projects into a local SQLite database, and exposes them as three read-only query commands — nothing leaves your machine.

**Primary use cases:**

- Understanding *why* code works the way it does (historical context from Git + Jira)
- Answering questions about past bugs, regressions, and feature decisions
- Checking whether a user-reported issue has been seen or addressed before

## Requirements

- **Node.js 20+** (22 recommended)
- **Git** on your `PATH` (used by `simple-git` for `fetch` and `log`)
- A C/C++ toolchain so `better-sqlite3` can compile its native binding on first install (Xcode Command Line Tools on macOS, `build-essential` on Debian/Ubuntu)
- A **Jira API token** if you want Jira data indexed ([create one](https://id.atlassian.com/manage-profile/security/api-tokens))

## Install & build

```sh
git clone <repo-url> readcube-scout
cd readcube-scout
npm install
npm run build
```

This produces `dist/query/index.js` (the query CLI entry point) and `dist/sync/index.js` (the sync CLI).

Pick one of the following to run the CLIs from anywhere:

```sh
# Option A: link the package globally (installs `readcube-scout` and `readcube-scout-sync` on PATH)
npm link

# Option B: run per-invocation via npx from the package directory
npx readcube-scout <subcommand>
npx readcube-scout-sync [options]

# Option C: run per-invocation via npx from anywhere (point to this directory)
npx --package=/path/to/readcube-scout readcube-scout <subcommand>
```

## Configuration

Create a `readcube-scout.config.json` with your Jira credentials and the list of projects to index. A template is provided:

```sh
mkdir -p ~/.readcube-scout
cp readcube-scout.config.example.json ~/.readcube-scout/config.json
# then edit it
```

**Config discovery order** (first match wins):

1. `readcube-scout.config.json` in the current directory or any ancestor directory
2. `.readcube-scout.json` in the current directory or any ancestor directory
3. `.config/readcube-scout/config.json` in the current directory or any ancestor directory
4. `~/.readcube-scout/config.json` (fallback)

The first three filenames are gitignored by default.

**Example:**

```json
{
  "dataDir": "~/.readcube-scout/data",
  "jira": {
    "host": "https://readcube.atlassian.net",
    "email": "you@readcube.com",
    "apiToken": "your-jira-api-token"
  },
  "projects": [
    {
      "name": "Papers",
      "gitPath": "~/dev/papers",
      "jiraProjectKey": "PAP",
      "gitRemote": "origin"
    }
  ]
}
```

**Fields:**

- `dataDir` — where the SQLite database (`knowledge.db`) is written. Created on first sync. Supports `~` expansion; relative paths resolve against the config file's directory.
- `jira.host` — your Atlassian Cloud base URL
- `jira.email` / `jira.apiToken` — credentials used as HTTP Basic auth against the Jira REST API
- `projects[].name` — a human label used in sync output and as the partition key in the database
- `projects[].gitPath` — path to a locally checked-out clone. Supports `~`.
- `projects[].jiraProjectKey` — the Jira project key scoping ticket fetches (e.g. `PAP`, `WEB`)
- `projects[].gitRemote` — remote to `git fetch` before indexing. Defaults to `origin`.

On startup the config is validated against a Zod schema; any errors are printed with the offending path (e.g. `projects.0.gitPath: gitPath is required`).

## First sync

Populate the local database by running the sync CLI at least once:

```sh
readcube-scout-sync            # full fetch on first run
readcube-scout-sync status     # confirm counts and last-synced timestamps per project/source
```

**Optional shortcut** — if you already have an indexed `knowledge.db` from a prior ReadCube indexing tool, the on-disk schema is byte-compatible. You can copy that file into `~/.readcube-scout/data/knowledge.db` instead of re-syncing from scratch.

## CLI reference

### `readcube-scout search <query>` — broad keyword lookup

Full-text search across all indexed Git commits and Jira tickets.

| Flag                    | Default | Description                                  |
| ----------------------- | ------- | -------------------------------------------- |
| `-p, --project <name>`  | —       | Restrict to a single configured project      |
| `-s, --source <source>` | `all`   | Which source to search: `git`, `jira`, `all` |
| `-l, --limit <n>`       | 20      | Max results (1–50)                           |

### `readcube-scout history <topic>` — chronological narrative

Unified timeline of commits and tickets for a topic/feature/file, oldest first, top-ranked only.

| Flag                     | Default | Description                                  |
| ------------------------ | ------- | -------------------------------------------- |
| `-p, --project <name>`   | —       | Restrict to a single configured project      |
| `--since <iso-date>`     | —       | ISO 8601 lower bound (e.g. `2024-01-01`)     |
| `-l, --limit <n>`        | 30      | Max results (1–100)                          |

### `readcube-scout related <description>` — similar historical tickets

Jira tickets most similar to a bug / behaviour description.

| Flag                    | Default | Description                                       |
| ----------------------- | ------- | ------------------------------------------------- |
| `-p, --project <name>`  | —       | Restrict to a single configured Jira project      |
| `-s, --status <bucket>` | `all`   | `open`, `resolved`, or `all`                      |
| `-l, --limit <n>`       | 10      | Max results (1–30)                                |

### `readcube-scout-sync [options]` — index refresh

```sh
readcube-scout-sync                         # sync everything
readcube-scout-sync -p Papers -s git        # one project, one source
readcube-scout-sync --full                  # force full Jira re-fetch
readcube-scout-sync status                  # show last-synced times + record counts
```

**What Git sync does:**

1. `git fetch <remote>` on the project's local path (does **not** touch your working tree or move local branches)
2. `git log --all` in a record-separator format to extract hash, author, ISO date, subject, body, and changed file paths across every branch
3. Upserts into the `commits` table and rebuilds the `commits_fts` index
4. Records a timestamp and commit count in `sync_state`

Git sync indexes **commit metadata only** — subjects, bodies, author, date, changed file paths. It does not index diff content or file contents.

**What Jira sync does:**

1. Paginated `POST /rest/api/3/search/jql` with explicit `fields` for summary, description, type, status, resolution, created, updated, reporter, assignee, labels
2. For each issue, paginated `GET /rest/api/3/issue/{key}/comment`
3. ADF (Atlassian Document Format) bodies on descriptions and comments are flattened to plain text for FTS indexing
4. Upserts into the `tickets` table and rebuilds the `tickets_fts` index
5. Records a timestamp and ticket count in `sync_state`

## Data & privacy

- The SQLite database lives at `<dataDir>/knowledge.db`. It is gitignored by default.
- Jira API tokens live only in your config file, which is gitignored by default.
- The query CLI never makes network calls. Only the sync CLI talks to Jira / your Git remotes.
- Consider excluding `<dataDir>` from iCloud, Dropbox, and other cloud sync — it mirrors internal codebase and Jira data.

## Repository layout

```
src/
├── config/
│   ├── loader.ts                     # Config file discovery + validation
│   └── schema.ts                     # Zod schema for readcube-scout.config.json
├── db/
│   ├── client.ts                     # SQLite connection + migrations runner
│   ├── queries.ts                    # searchCommits / searchTickets / sync state
│   └── migrations/
│       ├── 001_initial.sql           # commits, tickets, sync_state, FTS5 tables
│       └── 002_fts_porter_stemmer.sql
├── sync/
│   ├── index.ts                      # Sync CLI entry point (readcube-scout-sync)
│   ├── git.ts                        # Git log extraction + indexing
│   └── jira.ts                       # Jira issues + comments + indexing
├── query/
│   └── index.ts                      # Query CLI entry point (readcube-scout)
└── utils/
    ├── adf.ts                        # Atlassian Document Format → plain text
    ├── formatter.ts                  # Commit / ticket pretty-printers
    ├── fts.ts                        # User query → FTS5 MATCH expression
    └── logger.ts                     # stderr-only logger
```

## Development

```sh
npm run typecheck   # tsc --noEmit
npm run build       # tsc + copy SQL migrations to dist/
npm run sync        # run the sync CLI via tsx (no build step)
npm run query       # run the query CLI via tsx (no build step)
npm run clean       # rm -rf dist
```
