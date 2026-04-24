import type Database from 'better-sqlite3'
import { existsSync } from 'node:fs'
import { simpleGit, type SimpleGit } from 'simple-git'
import type { ProjectConfig } from '../config/schema.js'
import { logger } from '../utils/logger.js'

const RECORD_SEPARATOR = '\x1e'
const UNIT_SEPARATOR = '\x1f'
const LOG_FORMAT = `${RECORD_SEPARATOR}%H${UNIT_SEPARATOR}%an${UNIT_SEPARATOR}%aI${UNIT_SEPARATOR}%s${UNIT_SEPARATOR}%b${UNIT_SEPARATOR}`

interface ParsedCommit {
  hash: string
  author: string
  date: string
  subject: string
  body: string
  files: string[]
}

export interface GitSyncResult {
  project: string
  commitCount: number
}

export async function syncGitProject (
  db: Database.Database,
  project: ProjectConfig
): Promise<GitSyncResult> {
  if (!existsSync(project.gitPath)) {
    throw new Error(
      `gitPath "${project.gitPath}" does not exist for project "${project.name}"`
    )
  }

  const git = simpleGit(project.gitPath)

  logger.info(`[${project.name}] fetching ${project.gitRemote}`)
  try {
    await git.fetch(project.gitRemote)
  } catch (err) {
    throw new Error(
      `git fetch failed for project "${project.name}": ${(err as Error).message}`
    )
  }

  logger.info(`[${project.name}] reading commit history`)
  const commits = await readCommits(git)

  const upsertedCount = upsertCommits(db, project.name, commits)
  rebuildCommitsFts(db)
  const totalCount = countProjectCommits(db, project.name)
  updateSyncState(db, project.name, totalCount)

  logger.info(
    `[${project.name}] upserted ${upsertedCount} commit(s); ${totalCount} total in database`
  )
  return { project: project.name, commitCount: upsertedCount }
}

function countProjectCommits (db: Database.Database, projectName: string): number {
  const row = db
    .prepare('SELECT COUNT(*) AS n FROM commits WHERE project = ?')
    .get(projectName) as { n: number }
  return row.n
}

async function readCommits (git: SimpleGit): Promise<ParsedCommit[]> {
  const raw = await git.raw([
    'log',
    '--all',
    `--pretty=format:${LOG_FORMAT}`,
    '--name-only'
  ])

  return raw
    .split(RECORD_SEPARATOR)
    .filter((chunk) => chunk.length > 0)
    .map(parseRecord)
    .filter((commit): commit is ParsedCommit => commit !== null)
}

function parseRecord (record: string): ParsedCommit | null {
  const parts = record.split(UNIT_SEPARATOR)
  if (parts.length < 6) return null

  const [hash, author, date, subject, body, filesSection] = parts
  if (!hash || !author || !date) return null

  const files = (filesSection ?? '')
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line.length > 0)

  return {
    hash,
    author,
    date,
    subject: subject ?? '',
    body: body ?? '',
    files
  }
}

function upsertCommits (db: Database.Database, projectName: string, commits: ParsedCommit[]): number {
  const upsert = db.prepare(`
    INSERT INTO commits (id, project, author, date, message, body, files)
    VALUES (@id, @project, @author, @date, @message, @body, @files)
    ON CONFLICT(id) DO UPDATE SET
      project = excluded.project,
      author  = excluded.author,
      date    = excluded.date,
      message = excluded.message,
      body    = excluded.body,
      files   = excluded.files
  `)

  const run = db.transaction((rows: ParsedCommit[]) => {
    for (const row of rows) {
      upsert.run({
        id: row.hash,
        project: projectName,
        author: row.author,
        date: row.date,
        message: row.subject,
        body: row.body,
        files: JSON.stringify(row.files)
      })
    }
    return rows.length
  })

  return run(commits)
}

function rebuildCommitsFts (db: Database.Database): void {
  db.prepare("INSERT INTO commits_fts (commits_fts) VALUES ('rebuild')").run()
}

function updateSyncState (db: Database.Database, projectName: string, commitCount: number): void {
  db.prepare(`
    INSERT INTO sync_state (project, source, last_synced, commit_count, ticket_count)
    VALUES (
      @project,
      'git',
      @last_synced,
      @commit_count,
      (SELECT ticket_count FROM sync_state WHERE project = @project AND source = 'git')
    )
    ON CONFLICT(project, source) DO UPDATE SET
      last_synced  = excluded.last_synced,
      commit_count = excluded.commit_count
  `).run({
    project: projectName,
    last_synced: new Date().toISOString(),
    commit_count: commitCount
  })
}
