import type Database from 'better-sqlite3'

export interface CommitRow {
  id: string
  project: string
  author: string
  date: string
  message: string
  body: string | null
  files: string | null
  rank: number
}

export interface TicketRow {
  id: string
  project: string
  summary: string
  description: string | null
  type: string | null
  status: string | null
  resolution: string | null
  created_at: string | null
  updated_at: string | null
  reporter: string | null
  assignee: string | null
  labels: string | null
  comments: string | null
  rank: number
}

export interface SyncStateRow {
  project: string
  source: string
  last_synced: string
  commit_count: number | null
  ticket_count: number | null
  file_count: number | null
}

export interface CommitSearchOptions {
  project?: string
  since?: string
  limit: number
}

export interface TicketSearchOptions {
  project?: string
  since?: string
  status?: 'open' | 'resolved' | 'all'
  limit: number
}

export interface FileRow {
  project: string
  path: string
  language: string | null
  snippet: string
  rank: number
}

export interface FileSearchOptions {
  project?: string
  limit: number
}

export function searchCommits (
  db: Database.Database,
  ftsQuery: string,
  opts: CommitSearchOptions
): CommitRow[] {
  const clauses = ['commits_fts MATCH @query']
  const params: Record<string, unknown> = { query: ftsQuery, limit: opts.limit }

  if (opts.project !== undefined) {
    clauses.push('c.project = @project COLLATE NOCASE')
    params.project = opts.project
  }
  if (opts.since !== undefined) {
    clauses.push('c.date >= @since')
    params.since = opts.since
  }

  const sql = `
    SELECT c.id, c.project, c.author, c.date, c.message, c.body, c.files,
           bm25(commits_fts) AS rank
    FROM commits_fts
    JOIN commits c ON c.rowid = commits_fts.rowid
    WHERE ${clauses.join(' AND ')}
    ORDER BY rank
    LIMIT @limit
  `
  return db.prepare(sql).all(params) as CommitRow[]
}

export function searchTickets (
  db: Database.Database,
  ftsQuery: string,
  opts: TicketSearchOptions
): TicketRow[] {
  const clauses = ['tickets_fts MATCH @query']
  const params: Record<string, unknown> = { query: ftsQuery, limit: opts.limit }

  if (opts.project !== undefined) {
    clauses.push('t.project = @project COLLATE NOCASE')
    params.project = opts.project
  }
  if (opts.since !== undefined) {
    clauses.push('t.updated_at >= @since')
    params.since = opts.since
  }
  if (opts.status === 'open') {
    clauses.push("(t.resolution IS NULL OR t.resolution = '')")
  } else if (opts.status === 'resolved') {
    clauses.push("(t.resolution IS NOT NULL AND t.resolution != '')")
  }

  const sql = `
    SELECT t.id, t.project, t.summary, t.description, t.type, t.status, t.resolution,
           t.created_at, t.updated_at, t.reporter, t.assignee, t.labels, t.comments,
           bm25(tickets_fts) AS rank
    FROM tickets_fts
    JOIN tickets t ON t.rowid = tickets_fts.rowid
    WHERE ${clauses.join(' AND ')}
    ORDER BY rank
    LIMIT @limit
  `
  return db.prepare(sql).all(params) as TicketRow[]
}

export function getLastSynced (
  db: Database.Database,
  project: string,
  source: string
): string | undefined {
  const row = db
    .prepare('SELECT last_synced FROM sync_state WHERE project = ? AND source = ?')
    .get(project, source) as { last_synced: string } | undefined
  return row?.last_synced
}

export function getSyncState (db: Database.Database): SyncStateRow[] {
  return db.prepare(
    'SELECT project, source, last_synced, commit_count, ticket_count, file_count FROM sync_state ORDER BY project, source'
  ).all() as SyncStateRow[]
}

export function getTotals (db: Database.Database): { commits: number, tickets: number } {
  const commits = (db.prepare('SELECT COUNT(*) AS n FROM commits').get() as { n: number }).n
  const tickets = (db.prepare('SELECT COUNT(*) AS n FROM tickets').get() as { n: number }).n
  return { commits, tickets }
}

export function searchFiles (
  db: Database.Database,
  ftsQuery: string,
  opts: FileSearchOptions
): FileRow[] {
  const clauses = ['files_fts MATCH @query']
  const params: Record<string, unknown> = { query: ftsQuery, limit: opts.limit }

  if (opts.project !== undefined) {
    clauses.push('f.project = @project COLLATE NOCASE')
    params.project = opts.project
  }

  const sql = `
    SELECT f.project, f.path, f.language,
           snippet(files_fts, 1, '«', '»', '…', 16) AS snippet,
           bm25(files_fts) AS rank
    FROM files_fts
    JOIN files f ON f.rowid = files_fts.rowid
    WHERE ${clauses.join(' AND ')}
    ORDER BY rank
    LIMIT @limit
  `
  return db.prepare(sql).all(params) as FileRow[]
}
