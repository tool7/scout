#!/usr/bin/env node
import { Command } from 'commander'
import { resolve } from 'node:path'
import { loadConfig } from '../config/loader.js'
import { openDatabase } from '../db/client.js'
import type { Config, ProjectConfig } from '../config/schema.js'
import { logger } from '../utils/logger.js'
import { getSyncState, getTotals, type SyncStateRow } from '../db/queries.js'
import { syncGitProject } from './git.js'
import { syncJiraProject } from './jira.js'
import { syncCodeProject } from './code.js'

type Source = 'git' | 'jira' | 'code' | 'all'

interface SyncOptions {
  project?: string
  source?: string
  full?: boolean
}

function selectProjects (config: Config, name?: string): ProjectConfig[] {
  if (!name) return config.projects
  const match = config.projects.filter((project) => project.name === name)
  if (match.length === 0) {
    throw new Error(
      `No project named "${name}" found. Configured projects: ` +
      config.projects.map((project) => project.name).join(', ')
    )
  }
  return match
}

function parseSource (value: string | undefined): Source {
  const source = (value ?? 'all').toLowerCase()
  if (source !== 'git' && source !== 'jira' && source !== 'code' && source !== 'all') {
    throw new Error(`--source must be one of: git, jira, code, all (got "${value}")`)
  }
  return source
}

async function runSync (options: SyncOptions): Promise<void> {
  const config = await loadConfig()
  const source = parseSource(options.source)
  const full = options.full === true
  const projects = selectProjects(config, options.project)
  const dbPath = resolve(config.dataDir, 'knowledge.db')
  const db = openDatabase(dbPath)
  const fetchedSet = new Set<string>()

  try {
    for (const project of projects) {
      if (source === 'git' || source === 'all') {
        await syncGitProject(db, project, fetchedSet)
      }
      if (source === 'jira' || source === 'all') {
        await syncJiraProject(db, project, config.jira, { full })
      }
      if (source === 'code' || source === 'all') {
        await syncCodeProject(db, project, fetchedSet, { full })
      }
    }
  } finally {
    db.close()
  }
}

async function runStatus (): Promise<void> {
  const config = await loadConfig()
  const dbPath = resolve(config.dataDir, 'knowledge.db')
  const db = openDatabase(dbPath)

  try {
    const totals = getTotals(db)
    const states = getSyncState(db)
    printStatus(config, dbPath, totals, states)
  } finally {
    db.close()
  }
}

function printStatus (
  config: Config,
  dbPath: string,
  totals: { commits: number, tickets: number },
  states: SyncStateRow[]
): void {
  process.stdout.write(`Database:  ${dbPath}\n`)
  process.stdout.write(`Totals:    ${totals.commits} commit(s), ${totals.tickets} ticket(s)\n\n`)

  const byProject = new Map<string, Map<string, SyncStateRow>>()
  for (const state of states) {
    const sources = byProject.get(state.project) ?? new Map<string, SyncStateRow>()
    sources.set(state.source, state)
    byProject.set(state.project, sources)
  }

  const rows: Array<[string, string, string, string]> = [['project', 'source', 'last synced', 'records']]
  for (const project of config.projects) {
    for (const source of ['git', 'jira', 'code'] as const) {
      const state = byProject.get(project.name)?.get(source)
      const synced = state?.last_synced ?? '(never)'
      const records = state == null
        ? '-'
        : source === 'git'
          ? String(state.commit_count ?? 0)
          : source === 'jira'
            ? String(state.ticket_count ?? 0)
            : String(state.file_count ?? 0)
      rows.push([project.name, source, synced, records])
    }
  }

  const widths = rows[0]!.map((_, columnIndex) =>
    Math.max(...rows.map((row) => row[columnIndex]!.length))
  )

  for (const [index, row] of rows.entries()) {
    const line = row.map((cell, columnIndex) => cell.padEnd(widths[columnIndex]!)).join('  ')
    process.stdout.write(line + '\n')
    if (index === 0) {
      process.stdout.write(widths.map((width) => '-'.repeat(width)).join('  ') + '\n')
    }
  }
}

const program = new Command()

program
  .name('readcube-scout-sync')
  .description('Sync local Git and Jira data into the ReadCube Scout knowledge base')
  .version('0.1.0')

program
  .command('sync', { isDefault: true })
  .description('Run a sync across all configured projects (or a single project / source)')
  .option('-p, --project <name>', 'Only sync the named project')
  .option('-s, --source <source>', 'Only sync a specific source: git | jira | code | all', 'all')
  .option('-f, --full', 'Force a full Jira/code re-fetch instead of incremental (no-op for git)', false)
  .action(async (options: SyncOptions) => {
    try {
      await runSync(options)
    } catch (err) {
      logger.error((err as Error).message)
      process.exitCode = 1
    }
  })

program
  .command('status')
  .description('Show last sync time and record counts per project / source')
  .action(async () => {
    try {
      await runStatus()
    } catch (err) {
      logger.error((err as Error).message)
      process.exitCode = 1
    }
  })

await program.parseAsync(process.argv)
