import type { CommitRow, FileRow, TicketRow } from '../db/queries.js'

const MAX_BODY_CHARS = 400
const MAX_DESCRIPTION_CHARS = 400
const MAX_COMMENT_CHARS = 200
const MAX_FILES_SHOWN = 8
const MAX_COMMENTS_SHOWN = 3

interface TicketComment {
  author: string
  body: string
  created: string
}

function truncate (text: string, max: number): string {
  if (text.length <= max) return text
  return text.slice(0, max).trimEnd() + '…'
}

function shortHash (sha: string): string {
  return sha.slice(0, 8)
}

function isoDay (timestamp: string | null | undefined): string {
  if (timestamp == null || timestamp.length < 10) return ''
  return timestamp.slice(0, 10)
}

function parseJsonArray<T> (raw: string | null): T[] {
  if (raw === null || raw.length === 0) return []
  try {
    const parsed = JSON.parse(raw)
    return Array.isArray(parsed) ? parsed as T[] : []
  } catch {
    return []
  }
}

function joinNonEmpty (parts: Array<string | null | undefined>, separator: string): string {
  return parts.filter((part): part is string => part != null && part.length > 0).join(separator)
}

export function formatCommit (commit: CommitRow): string {
  const header = `[commit · ${commit.project}] ${shortHash(commit.id)} · ${isoDay(commit.date)} · ${commit.author}`
  const body = commit.body?.trim() ?? ''
  const bodyLine = body.length === 0 ? '' : '\n' + truncate(body, MAX_BODY_CHARS)

  const files = parseJsonArray<string>(commit.files)
  const filesLine = files.length === 0
    ? ''
    : '\nFiles: ' + files.slice(0, MAX_FILES_SHOWN).join(', ') +
      (files.length > MAX_FILES_SHOWN ? ` (+${files.length - MAX_FILES_SHOWN} more)` : '')

  return `${header}\n${commit.message}${bodyLine}${filesLine}`
}

export function formatTicket (ticket: TicketRow, includeComments: boolean): string {
  const typeStatus = joinNonEmpty([ticket.type, ticket.status], ' · ')
  const resolution = ticket.resolution != null && ticket.resolution.length > 0
    ? ` · resolved: ${ticket.resolution}`
    : ''
  const assignee = ticket.assignee != null && ticket.assignee.length > 0
    ? ` · assignee: ${ticket.assignee}`
    : ''
  const updated = ticket.updated_at != null && ticket.updated_at.length > 0
    ? ` · updated ${isoDay(ticket.updated_at)}`
    : ''

  const header = `[jira · ${ticket.project}] ${ticket.id}${typeStatus.length > 0 ? ' · ' + typeStatus : ''}${resolution}${assignee}${updated}`
  const summary = `Summary: ${ticket.summary}`

  const description = ticket.description?.trim() ?? ''
  const descriptionLine = description.length === 0 ? '' : '\n' + truncate(description, MAX_DESCRIPTION_CHARS)

  if (!includeComments) return `${header}\n${summary}${descriptionLine}`

  const comments = parseJsonArray<TicketComment>(ticket.comments)
  if (comments.length === 0) return `${header}\n${summary}${descriptionLine}`

  const latest = comments.slice(-MAX_COMMENTS_SHOWN)
  const commentLines = latest
    .map((comment) => `  - ${comment.author} (${isoDay(comment.created)}): ${truncate(comment.body.replace(/\s+/g, ' '), MAX_COMMENT_CHARS)}`)
    .join('\n')
  const commentsHeader = comments.length > MAX_COMMENTS_SHOWN
    ? `Recent comments (last ${MAX_COMMENTS_SHOWN} of ${comments.length}):`
    : 'Comments:'

  return `${header}\n${summary}${descriptionLine}\n${commentsHeader}\n${commentLines}`
}

export function formatFile (file: FileRow): string {
  const lang = file.language != null && file.language.length > 0
    ? ` · ${file.language}`
    : ''
  const header = `[code · ${file.project}${lang}] ${file.path}`
  return `${header}\n${file.snippet.trim()}`
}
