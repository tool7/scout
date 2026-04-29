# scout

A local CLI that gives developers and QA engineers a conversational interface into a project's domain knowledge. It indexes **Git history**, **Jira tickets**, and **source code** across a configurable set of projects into a local SQLite database, and exposes them as read-only query commands — nothing leaves your machine.

**Primary use cases:**

- Understanding *why* code works the way it does (historical context from Git + Jira)
- Answering questions about past bugs, regressions, and feature decisions
- Checking whether a user-reported issue has been seen or addressed before

## Requirements

- **Go 1.22+** to build (no Go runtime needed at runtime — the result is a single static binary)
- **Git** on your `PATH` (used for `fetch`, `log`, `ls-tree`, and `cat-file`)
- A **Jira API token** if you want Jira data indexed ([create one](https://id.atlassian.com/manage-profile/security/api-tokens))

The SQLite driver is pure Go (`modernc.org/sqlite`), so no C/C++ toolchain is required.

## Install & build

```sh
git clone <repo-url> scout
cd scout
go build ./cmd/scout
```

This produces a single `scout` executable in the current directory. Alternatively:

```sh
# Install onto $GOPATH/bin (or $GOBIN if set)
go install ./cmd/scout

# Or run without building a binary
go run ./cmd/scout <subcommand>
```

## Configuration

Create a `scout.config.json` with your Jira credentials and the list of projects to index. A template is provided:

```sh
mkdir -p ~/.scout
cp scout.config.example.json ~/.scout/config.json
# then edit it
```

The CLI also accepts project-local configs (`scout.config.json`, `.scout.json`, or `.config/scout/config.json`) discovered by walking up from the current directory; the home-directory file is the fallback when none is found.

**Example:**

```json
{
  "dataDir": "~/.scout/data",
  "jira": {
    "host": "https://your-org.atlassian.net",
    "email": "you@example.com",
    "apiToken": "your-jira-api-token"
  },
  "projects": [
    {
      "name": "ExampleProject",
      "gitPath": "/Users/<username>/Projects/example-project",
      "jiraProjectKey": "EXAMPLE",
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
- `projects[].jiraProjectKey` — the Jira project key scoping ticket fetches (e.g. `EXAMPLE`, `PROJ`)
- `projects[].gitRemote` — remote to `git fetch` before indexing. Defaults to `origin`.
- `projects[].indexRef` — *(optional)* Git ref to index source code from. Defaults to the result of `git symbolic-ref --short refs/remotes/origin/HEAD` (i.e. the configured default branch — typically `origin/master` or `origin/main`). Set explicitly only if your project ships from a non-default branch.
- `projects[].excludePaths` — *(optional)* array of `gitignore`-style globs matched against repo-relative paths during code sync. Empty by default. Globs are evaluated by [doublestar](https://github.com/bmatcuk/doublestar); leading `/` is not significant.

On startup the config is validated; any errors are printed with the offending path (e.g. `projects.0.gitPath: gitPath is required`).

## First sync

Populate the local database by running the sync subcommand at least once:

```sh
scout sync            # full fetch on first run
scout status          # confirm counts and last-synced timestamps per project/source
```

**Optional shortcut** — if you already have an indexed `knowledge.db` from a prior compatible indexing tool, the on-disk schema is byte-compatible. You can copy that file into `~/.scout/data/knowledge.db` instead of re-syncing from scratch.

## CLI reference

### `scout search <query>` — broad keyword lookup

Full-text search across all indexed Git commits, Jira tickets, and source-code files.

| Flag                    | Default | Description                                  |
| ----------------------- | ------- | -------------------------------------------- |
| `-p, --project <name>`  | —       | Restrict to a single configured project      |
| `-s, --source <source>` | `all`   | Which source to search: `git`, `jira`, `code`, `all` |
| `-l, --limit <n>`       | 20      | Max results (1–50)                           |

### `scout history <topic>` — chronological narrative

Unified timeline of commits and tickets for a topic/feature/file, oldest first, top-ranked only.

| Flag                     | Default | Description                                  |
| ------------------------ | ------- | -------------------------------------------- |
| `-p, --project <name>`   | —       | Restrict to a single configured project      |
| `--since <iso-date>`     | —       | ISO 8601 lower bound (e.g. `2024-01-01`)     |
| `-l, --limit <n>`        | 30      | Max results (1–100)                          |

### `scout related <description>` — similar historical tickets

Jira tickets most similar to a bug / behaviour description.

| Flag                    | Default | Description                                       |
| ----------------------- | ------- | ------------------------------------------------- |
| `-p, --project <name>`  | —       | Restrict to a single configured Jira project      |
| `-s, --status <bucket>` | `all`   | `open`, `resolved`, or `all`                      |
| `-l, --limit <n>`       | 10      | Max results (1–30)                                |

### `scout sync [options]` — index refresh

```sh
scout sync                                   # sync everything
scout sync -p ExampleProject -s git          # one project, one source
scout sync -s code                           # only refresh source-code indexes
scout sync --full                            # force full Jira/code re-fetch
```

| Flag                    | Default | Description                                       |
| ----------------------- | ------- | ------------------------------------------------- |
| `-p, --project <name>`  | —       | Only sync the named project                       |
| `-s, --source <source>` | `all`   | Only sync a specific source: `git`, `jira`, `code`, `all` |
| `-f, --full`            | false   | Force a full Jira/code re-fetch (no-op for git)   |

### `scout status` — sync state

Show last sync time and record counts per project / source.

```sh
scout status
```

### `scout --instructions` — full machine- and human-readable usage reference

Prints the authoritative, version-matched usage reference for every sub-command, flag, default, range, and exit code. Useful for both humans (one place to read everything) and AI agents (a stable contract that travels with the binary).

```sh
scout --instructions
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

**What Code sync does:**

1. `git fetch <remote>` (deduplicated with the commit sync — runs at most once per project per `--source all`)
2. Resolves the indexed ref: `projects[].indexRef` if set, otherwise `git symbolic-ref --short refs/remotes/origin/HEAD`
3. `git ls-tree -r --long <ref>` lists every tracked path with its blob hash and size
4. Diffs that against `files` for the same project, only fetching blobs (`git cat-file -p <hash>`) for new or changed paths
5. Filters out paths matching `excludePaths`, the built-in lockfile / source-map / minified-bundle denylist, files larger than 1 MiB, and bytes that don't decode as valid UTF-8
6. Upserts survivors into `files`; SQLite triggers keep the trigram-tokenized `files_fts` index in sync
7. Records a timestamp and file count in `sync_state`

Code sync indexes **file contents** as committed at the configured ref. It does not see uncommitted local changes or alternate branches.

## Data & privacy

- The SQLite database lives at `<dataDir>/knowledge.db`. It is gitignored by default.
- Jira API tokens live only in your config file, which is gitignored by default.
- The query subcommands (`search`, `history`, `related`, `status`) never make network calls. Only `sync` talks to Jira / your Git remotes.

## Repository layout

```
cmd/
└── scout/
    └── main.go                      # single binary entry point
internal/
├── cli/                             # Cobra root + sub-commands (search, history, related, sync, status)
├── config/                          # config discovery, validation, ~/relative path resolution
├── db/
│   ├── client.go                    # SQLite open + migrations runner (modernc.org/sqlite)
│   ├── queries.go                   # SearchCommits / SearchTickets / SearchFiles / sync state
│   └── migrations/
│       ├── 001_initial.sql          # commits, tickets, sync_state, FTS5 tables
│       ├── 002_fts_porter_stemmer.sql
│       └── 003_files.sql            # files table, files_fts (trigram), file_count column
├── sync/
│   ├── git.go                       # Git fetch (shared) + commit log extraction + indexing
│   ├── jira.go                      # Jira issues + comments + indexing
│   └── code.go                      # Source-code tree walk + filter ladder + indexing
├── adf/                             # Atlassian Document Format → plain text
├── format/                          # Commit / ticket / file pretty-printers
├── fts/                             # User query → FTS5 MATCH expression (natural + code modes)
└── logger/                          # stderr-only logger
```

## Development

```sh
go build ./cmd/scout                # build the binary
go run ./cmd/scout sync             # run sync without installing
go run ./cmd/scout search "..."
go vet ./...                        # static analysis
go fmt ./...                        # format
go test ./...                       # run tests (none yet — placeholder for future contributors)
```
