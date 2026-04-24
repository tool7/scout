import axios, { type AxiosInstance } from 'axios'
import type Database from 'better-sqlite3'
import type { JiraConfig, ProjectConfig } from '../config/schema.js'
import { getLastSynced } from '../db/queries.js'
import { adfToPlain } from '../utils/adf.js'
import { logger } from '../utils/logger.js'

const INCREMENTAL_OVERLAP_MINUTES = 10

const SEARCH_FIELDS = [
  'summary',
  'description',
  'issuetype',
  'status',
  'resolution',
  'created',
  'updated',
  'reporter',
  'assignee',
  'labels',
  'comment'
]

const SEARCH_PAGE_SIZE = 100

interface JiraIssue {
  id: string
  key: string
  fields: Record<string, unknown>
}

interface JiraComment {
  id: string
  author?: { displayName?: string, emailAddress?: string }
  body?: unknown
  created?: string
  updated?: string
}

interface ParsedTicket {
  id: string
  summary: string
  description: string
  type: string
  status: string
  resolution: string
  created_at: string
  updated_at: string
  reporter: string
  assignee: string
  labels: string[]
  comments: Array<{ author: string, body: string, created: string }>
}

export interface JiraSyncResult {
  project: string
  ticketCount: number
}

export interface JiraSyncOptions {
  full: boolean
}

export async function syncJiraProject (
  db: Database.Database,
  project: ProjectConfig,
  jira: JiraConfig,
  options: JiraSyncOptions = { full: false }
): Promise<JiraSyncResult> {
  const client = createClient(jira)

  const since = options.full ? undefined : resolveIncrementalSince(db, project.name)
  const mode = since === undefined ? 'full' : `incremental since ${since} UTC`
  logger.info(`[${project.name}] fetching Jira issues for project ${project.jiraProjectKey} (${mode})`)

  const issues = await fetchAllIssues(client, project.jiraProjectKey, since)
  logger.info(`[${project.name}] fetched ${issues.length} issues (comments inline)`)

  let truncated = 0
  const tickets = issues.map((issue) => {
    const extracted = extractInlineComments(issue)
    if (extracted.truncated) truncated++
    return toTicket(issue, extracted.comments)
  })

  if (truncated > 0) {
    logger.warn(
      `[${project.name}] ${truncated} ticket(s) had more comments than the inline page; ` +
      'only the first page is indexed'
    )
  }

  const upsertedCount = upsertTickets(db, project.name, tickets)
  rebuildTicketsFts(db)
  const totalCount = countProjectTickets(db, project.name)
  updateSyncState(db, project.name, totalCount)

  logger.info(
    `[${project.name}] upserted ${upsertedCount} ticket(s); ${totalCount} total in database`
  )
  return { project: project.name, ticketCount: upsertedCount }
}

function createClient (jira: JiraConfig): AxiosInstance {
  return axios.create({
    baseURL: `${jira.host.replace(/\/+$/, '')}/rest/api/3`,
    auth: { username: jira.email, password: jira.apiToken },
    headers: { Accept: 'application/json' },
    timeout: 30_000
  })
}

async function fetchAllIssues (
  client: AxiosInstance,
  projectKey: string,
  since: string | undefined
): Promise<JiraIssue[]> {
  const issues: JiraIssue[] = []
  let nextPageToken: string | undefined

  const jqlClauses = [`project = "${projectKey}"`]
  if (since !== undefined) jqlClauses.push(`updated >= "${since}"`)
  const jql = `${jqlClauses.join(' AND ')} ORDER BY updated DESC`

  do {
    const body: Record<string, unknown> = {
      jql,
      fields: SEARCH_FIELDS,
      maxResults: SEARCH_PAGE_SIZE
    }
    if (nextPageToken !== undefined) body.nextPageToken = nextPageToken

    const response = await request<{
      issues: JiraIssue[]
      nextPageToken?: string
      isLast?: boolean
    }>(() => client.post('/search/jql', body), 'search issues')

    issues.push(...response.issues)
    nextPageToken = response.isLast === true ? undefined : response.nextPageToken
  } while (nextPageToken !== undefined)

  return issues
}

function resolveIncrementalSince (db: Database.Database, projectName: string): string | undefined {
  const lastSynced = getLastSynced(db, projectName, 'jira')
  if (lastSynced === undefined) return undefined
  return toJqlTimestamp(lastSynced, INCREMENTAL_OVERLAP_MINUTES)
}

function toJqlTimestamp (iso: string, overlapMinutes: number): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) {
    throw new Error(`Invalid last_synced timestamp: ${iso}`)
  }
  date.setUTCMinutes(date.getUTCMinutes() - overlapMinutes)
  const pad = (n: number): string => String(n).padStart(2, '0')
  const year = date.getUTCFullYear()
  const month = pad(date.getUTCMonth() + 1)
  const day = pad(date.getUTCDate())
  const hours = pad(date.getUTCHours())
  const minutes = pad(date.getUTCMinutes())
  return `${year}-${month}-${day} ${hours}:${minutes}`
}

function extractInlineComments (issue: JiraIssue): { comments: JiraComment[], truncated: boolean } {
  const field = issue.fields.comment
  if (field == null || typeof field !== 'object') {
    return { comments: [], truncated: false }
  }

  const envelope = field as Record<string, unknown>
  const comments = Array.isArray(envelope.comments)
    ? (envelope.comments as JiraComment[])
    : []
  const total = typeof envelope.total === 'number' ? envelope.total : comments.length

  return { comments, truncated: total > comments.length }
}

async function request<T> (fn: () => Promise<{ data: T }>, label: string): Promise<T> {
  try {
    const response = await fn()
    return response.data
  } catch (err) {
    throw new Error(`Jira API call failed (${label}): ${formatApiError(err)}`)
  }
}

function formatApiError (err: unknown): string {
  if (axios.isAxiosError(err)) {
    const status = err.response?.status ?? 'no-status'
    const data = err.response?.data
    let detail = ''
    if (data != null) {
      try {
        detail = typeof data === 'string' ? data : JSON.stringify(data)
      } catch {
        detail = '[unserializable response body]'
      }
    }
    return `HTTP ${status} ${detail}`.trim()
  }
  return (err as Error).message
}

function toTicket (issue: JiraIssue, comments: JiraComment[]): ParsedTicket {
  const fields = issue.fields
  const type = getNested(fields, 'issuetype', 'name') ?? ''
  const status = getNested(fields, 'status', 'name') ?? ''
  const resolution = getNested(fields, 'resolution', 'name') ?? ''
  const reporter = getNested(fields, 'reporter', 'displayName') ?? ''
  const assignee = getNested(fields, 'assignee', 'displayName') ?? ''
  const labels = Array.isArray(fields.labels)
    ? fields.labels.filter((label): label is string => typeof label === 'string')
    : []

  return {
    id: issue.key,
    summary: typeof fields.summary === 'string' ? fields.summary : '',
    description: adfToPlain(fields.description),
    type,
    status,
    resolution,
    created_at: typeof fields.created === 'string' ? fields.created : '',
    updated_at: typeof fields.updated === 'string' ? fields.updated : '',
    reporter,
    assignee,
    labels,
    comments: comments.map((comment) => ({
      author: comment.author?.displayName ?? comment.author?.emailAddress ?? '',
      body: adfToPlain(comment.body),
      created: comment.created ?? ''
    }))
  }
}

function getNested (obj: Record<string, unknown>, key: string, nested: string): string | undefined {
  const value = obj[key]
  if (value == null || typeof value !== 'object') return undefined
  const inner = (value as Record<string, unknown>)[nested]
  return typeof inner === 'string' ? inner : undefined
}

function upsertTickets (db: Database.Database, projectName: string, tickets: ParsedTicket[]): number {
  const upsert = db.prepare(`
    INSERT INTO tickets (
      id, project, summary, description, type, status, resolution,
      created_at, updated_at, reporter, assignee, labels, comments
    ) VALUES (
      @id, @project, @summary, @description, @type, @status, @resolution,
      @created_at, @updated_at, @reporter, @assignee, @labels, @comments
    )
    ON CONFLICT(id) DO UPDATE SET
      project     = excluded.project,
      summary     = excluded.summary,
      description = excluded.description,
      type        = excluded.type,
      status      = excluded.status,
      resolution  = excluded.resolution,
      created_at  = excluded.created_at,
      updated_at  = excluded.updated_at,
      reporter    = excluded.reporter,
      assignee    = excluded.assignee,
      labels      = excluded.labels,
      comments    = excluded.comments
  `)

  const run = db.transaction((rows: ParsedTicket[]) => {
    for (const row of rows) {
      upsert.run({
        id: row.id,
        project: projectName,
        summary: row.summary,
        description: row.description,
        type: row.type,
        status: row.status,
        resolution: row.resolution,
        created_at: row.created_at,
        updated_at: row.updated_at,
        reporter: row.reporter,
        assignee: row.assignee,
        labels: JSON.stringify(row.labels),
        comments: JSON.stringify(row.comments)
      })
    }
    return rows.length
  })

  return run(tickets)
}

function rebuildTicketsFts (db: Database.Database): void {
  db.prepare("INSERT INTO tickets_fts (tickets_fts) VALUES ('rebuild')").run()
}

function countProjectTickets (db: Database.Database, projectName: string): number {
  const row = db
    .prepare('SELECT COUNT(*) AS n FROM tickets WHERE project = ?')
    .get(projectName) as { n: number }
  return row.n
}

function updateSyncState (db: Database.Database, projectName: string, ticketCount: number): void {
  db.prepare(`
    INSERT INTO sync_state (project, source, last_synced, commit_count, ticket_count)
    VALUES (
      @project,
      'jira',
      @last_synced,
      (SELECT commit_count FROM sync_state WHERE project = @project AND source = 'jira'),
      @ticket_count
    )
    ON CONFLICT(project, source) DO UPDATE SET
      last_synced  = excluded.last_synced,
      ticket_count = excluded.ticket_count
  `).run({
    project: projectName,
    last_synced: new Date().toISOString(),
    ticket_count: ticketCount
  })
}
