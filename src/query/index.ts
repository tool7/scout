#!/usr/bin/env node
import { Command, InvalidArgumentError } from 'commander'
import { resolve } from 'node:path'
import type Database from 'better-sqlite3'
import { loadConfig } from '../config/loader.js'
import { openDatabase } from '../db/client.js'
import { searchCommits, searchTickets, type CommitRow, type TicketRow } from '../db/queries.js'
import { toFtsQuery } from '../utils/fts.js'
import { formatCommit, formatTicket } from '../utils/formatter.js'
import { logger } from '../utils/logger.js'

type SourceFilter = 'git' | 'jira' | 'all'
type StatusFilter = 'open' | 'resolved' | 'all'

interface RankedEntry {
  kind: 'commit' | 'ticket'
  rank: number
  commit?: CommitRow
  ticket?: TicketRow
}

interface TimelineEvent {
  kind: 'commit' | 'ticket'
  rank: number
  date: string
  commit?: CommitRow
  ticket?: TicketRow
}

async function openContext (): Promise<{ db: Database.Database }> {
  const config = await loadConfig()
  const db = openDatabase(resolve(config.dataDir, 'knowledge.db'))
  return { db }
}

function emptyQueryExit (input: string): void {
  process.stderr.write(`No usable search terms in "${input}".\n`)
  process.exitCode = 1
}

function writeStdout (text: string): void {
  process.stdout.write(text + '\n')
}

function parseIntInRange (label: string, min: number, max: number): (value: string) => number {
  return (value) => {
    const n = Number.parseInt(value, 10)
    if (!Number.isFinite(n) || n < min || n > max) {
      throw new InvalidArgumentError(`${label} must be an integer between ${min} and ${max}`)
    }
    return n
  }
}

function parseEnum<T extends string> (label: string, allowed: readonly T[]): (value: string) => T {
  return (value) => {
    if (!allowed.includes(value as T)) {
      throw new InvalidArgumentError(`${label} must be one of: ${allowed.join(', ')}`)
    }
    return value as T
  }
}

interface SearchOptions {
  project?: string
  source: SourceFilter
  limit: number
}

async function runSearch (query: string, options: SearchOptions): Promise<void> {
  const ftsQuery = toFtsQuery(query)
  if (ftsQuery.length === 0) {
    emptyQueryExit(query)
    return
  }

  const { db } = await openContext()
  try {
    const { source, limit, project } = options
    const commits = source === 'jira'
      ? []
      : searchCommits(db, ftsQuery, { project, limit })
    const tickets = source === 'git'
      ? []
      : searchTickets(db, ftsQuery, { project, limit, status: 'all' })

    const entries: RankedEntry[] = [
      ...commits.map((row): RankedEntry => ({ kind: 'commit', rank: row.rank, commit: row })),
      ...tickets.map((row): RankedEntry => ({ kind: 'ticket', rank: row.rank, ticket: row }))
    ]

    entries.sort((a, b) => a.rank - b.rank)
    const top = entries.slice(0, limit)

    if (top.length === 0) {
      writeStdout(`No matches for "${query}".`)
      return
    }

    const header = `Found ${top.length} result(s) for "${query}"` +
      (project !== undefined ? ` in project "${project}"` : '') +
      (source !== 'all' ? ` (source: ${source})` : '') + ':'

    const items = top.map((entry, index) => {
      const body = entry.kind === 'commit' && entry.commit !== undefined
        ? formatCommit(entry.commit)
        : entry.ticket !== undefined
          ? formatTicket(entry.ticket, false)
          : ''
      return `${index + 1}. ${body}`
    })

    writeStdout([header, ...items].join('\n\n'))
  } finally {
    db.close()
  }
}

interface HistoryOptions {
  project?: string
  since?: string
  limit: number
}

async function runHistory (topic: string, options: HistoryOptions): Promise<void> {
  const ftsQuery = toFtsQuery(topic)
  if (ftsQuery.length === 0) {
    emptyQueryExit(topic)
    return
  }

  const { db } = await openContext()
  try {
    const { project, since, limit } = options

    const commits = searchCommits(db, ftsQuery, { project, since, limit })
    const tickets = searchTickets(db, ftsQuery, { project, since, limit, status: 'all' })

    const events: TimelineEvent[] = [
      ...commits.map((row): TimelineEvent => ({
        kind: 'commit', rank: row.rank, date: row.date, commit: row
      })),
      ...tickets.map((row): TimelineEvent => ({
        kind: 'ticket', rank: row.rank, date: row.updated_at ?? row.created_at ?? '', ticket: row
      }))
    ]

    events.sort((a, b) => a.rank - b.rank)
    const topByRelevance = events.slice(0, limit)

    topByRelevance.sort((a, b) => a.date.localeCompare(b.date))

    if (topByRelevance.length === 0) {
      writeStdout(`No matches for "${topic}".`)
      return
    }

    const header = `Timeline for "${topic}" — ${topByRelevance.length} event(s), oldest first` +
      (project !== undefined ? ` (project: ${project})` : '') +
      (since !== undefined ? ` (since ${since.slice(0, 10)})` : '') + ':'

    const items = topByRelevance.map((event) => {
      const day = event.date.length >= 10 ? event.date.slice(0, 10) : '????-??-??'
      const body = event.kind === 'commit' && event.commit !== undefined
        ? formatCommit(event.commit)
        : event.ticket !== undefined
          ? formatTicket(event.ticket, false)
          : ''
      return `— ${day} —\n${body}`
    })

    writeStdout([header, ...items].join('\n\n'))
  } finally {
    db.close()
  }
}

interface RelatedOptions {
  project?: string
  status: StatusFilter
  limit: number
}

async function runRelated (description: string, options: RelatedOptions): Promise<void> {
  const ftsQuery = toFtsQuery(description)
  if (ftsQuery.length === 0) {
    emptyQueryExit(description)
    return
  }

  const { db } = await openContext()
  try {
    const { project, status, limit } = options
    const tickets = searchTickets(db, ftsQuery, { project, status, limit })

    if (tickets.length === 0) {
      writeStdout(`No matches for "${description}".`)
      return
    }

    const header = `Found ${tickets.length} related ticket(s)` +
      (status !== 'all' ? ` (status: ${status})` : '') +
      (project !== undefined ? ` in project "${project}"` : '') + ':'

    const items = tickets.map((ticket, index) => `${index + 1}. ${formatTicket(ticket, true)}`)
    writeStdout([header, ...items].join('\n\n'))
  } finally {
    db.close()
  }
}

const program = new Command()

program
  .name('readcube-scout')
  .description('Query the local ReadCube Scout knowledge base (indexed Git commits + Jira tickets)')
  .version('0.1.0')

program
  .command('search')
  .description('Broad full-text search across Git commits and Jira tickets')
  .argument('<query>', 'Natural language or keyword query')
  .option('-p, --project <name>', 'Only search within one project')
  .option(
    '-s, --source <source>',
    'Which source to search: git | jira | all',
    parseEnum('--source', ['git', 'jira', 'all'] as const),
    'all' as SourceFilter
  )
  .option(
    '-l, --limit <n>',
    'Maximum results to return (1-50)',
    parseIntInRange('--limit', 1, 50),
    20
  )
  .action(async (query: string, options: SearchOptions) => {
    try {
      await runSearch(query, options)
    } catch (err) {
      logger.error((err as Error).message)
      process.exitCode = 1
    }
  })

program
  .command('history')
  .description('Unified chronological timeline for a topic (commits + tickets, oldest first)')
  .argument('<topic>', 'Feature name, file path, component name, or keyword')
  .option('-p, --project <name>', 'Only search within one project')
  .option('--since <iso-date>', 'ISO 8601 lower bound (e.g. 2024-01-01)')
  .option(
    '-l, --limit <n>',
    'Maximum results to return (1-100)',
    parseIntInRange('--limit', 1, 100),
    30
  )
  .action(async (topic: string, options: HistoryOptions) => {
    try {
      await runHistory(topic, options)
    } catch (err) {
      logger.error((err as Error).message)
      process.exitCode = 1
    }
  })

program
  .command('related')
  .description('Find Jira tickets similar to a bug or behaviour description')
  .argument('<description>', 'Free-text description of the issue or behaviour')
  .option('-p, --project <name>', 'Only search within one Jira project')
  .option(
    '-s, --status <bucket>',
    'Filter by status: open | resolved | all',
    parseEnum('--status', ['open', 'resolved', 'all'] as const),
    'all' as StatusFilter
  )
  .option(
    '-l, --limit <n>',
    'Maximum results to return (1-30)',
    parseIntInRange('--limit', 1, 30),
    10
  )
  .action(async (description: string, options: RelatedOptions) => {
    try {
      await runRelated(description, options)
    } catch (err) {
      logger.error((err as Error).message)
      process.exitCode = 1
    }
  })

await program.parseAsync(process.argv)
