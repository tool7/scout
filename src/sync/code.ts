import type Database from 'better-sqlite3'
import picomatch from 'picomatch'
import { spawn } from 'node:child_process'
import { extname } from 'node:path'
import type { SimpleGit } from 'simple-git'
import type { ProjectConfig } from '../config/schema.js'
import { logger } from '../utils/logger.js'
import { gitFetchOnce } from './git.js'

const MAX_FILE_BYTES = 1 * 1024 * 1024 // 1 MiB

const EXTENSION_DENYLIST = new Set<string>([
  '.lock',
  '.map',
  '.snap',
  '.min.js',
  '.min.css'
])

const FILENAME_DENYLIST = new Set<string>([
  'package-lock.json',
  'yarn.lock',
  'pnpm-lock.yaml',
  'Gemfile.lock',
  'go.sum',
  'poetry.lock',
  'Cargo.lock'
])

const EXTENSION_LANGUAGE: Record<string, string> = {
  '.ts':   'ts',
  '.tsx':  'tsx',
  '.js':   'js',
  '.jsx':  'jsx',
  '.mjs':  'js',
  '.cjs':  'js',
  '.rb':   'ruby',
  '.go':   'go',
  '.py':   'python',
  '.rs':   'rust',
  '.java': 'java',
  '.kt':   'kotlin',
  '.swift':'swift',
  '.c':    'c',
  '.h':    'c',
  '.cpp':  'cpp',
  '.hpp':  'cpp',
  '.cs':   'csharp',
  '.sql':  'sql',
  '.sh':   'shell',
  '.bash': 'shell',
  '.zsh':  'shell',
  '.md':   'markdown',
  '.json': 'json',
  '.yml':  'yaml',
  '.yaml': 'yaml',
  '.toml': 'toml',
  '.html': 'html',
  '.css':  'css',
  '.scss': 'scss'
}

interface TreeEntry {
  blobHash: string
  sizeBytes: number
  path: string
}

interface PendingUpsert {
  project: string
  path: string
  blob_hash: string
  size_bytes: number
  language: string | null
  content: string
  indexed_at: string
}

export interface CodeSyncResult {
  project: string
  fileCount: number
}

export interface CodeSyncOptions {
  full?: boolean
}

export async function syncCodeProject (
  db: Database.Database,
  project: ProjectConfig,
  fetchedSet: Set<string>,
  options: CodeSyncOptions = {}
): Promise<CodeSyncResult> {
  const full = options.full === true

  const git = await gitFetchOnce(project, fetchedSet)
  const ref = await resolveIndexRef(git, project)

  if (full) {
    db.prepare('DELETE FROM files WHERE project = ?').run(project.name)
  }

  logger.info(`[${project.name}] reading tree at ${ref}`)
  const tree = await readTree(git, ref)

  const indexed = readIndexedHashes(db, project.name)
  const isExcluded = makeExcludeMatcher(project.excludePaths)

  let inserted = 0
  let updated = 0
  let skipped = 0

  const upsert = db.prepare(`
    INSERT INTO files (project, path, blob_hash, size_bytes, language, content, indexed_at)
    VALUES (@project, @path, @blob_hash, @size_bytes, @language, @content, @indexed_at)
    ON CONFLICT(project, path) DO UPDATE SET
      blob_hash  = excluded.blob_hash,
      size_bytes = excluded.size_bytes,
      language   = excluded.language,
      content    = excluded.content,
      indexed_at = excluded.indexed_at
  `)
  const remove = db.prepare('DELETE FROM files WHERE project = ? AND path = ?')

  // Phase 1 (async): walk the tree and decide what to write. We can't use
  // db.transaction() here because better-sqlite3 transactions are
  // synchronous-only and `fetchBlob` awaits a child process. We accumulate
  // upsert/delete intents and flush them atomically in Phase 2.
  const pendingUpserts: PendingUpsert[] = []

  for (const entry of tree) {
    const existingHash = indexed.get(entry.path)
    if (existingHash === entry.blobHash) {
      indexed.delete(entry.path)
      continue
    }

    if (preFilter(entry, isExcluded)) {
      // If a previously-indexed file now matches an exclude or denylist rule,
      // we want it gone from the index. The path stays in `indexed` here so
      // the post-loop sweep removes it from the DB.
      skipped += 1
      continue
    }

    if (!isValidBlobHash(entry.blobHash)) {
      logger.warn(`[${project.name}] skipping ${entry.path}: invalid blob hash "${entry.blobHash}"`)
      skipped += 1
      continue
    }

    const bytes = await fetchBlob(project.gitPath, entry.blobHash)
    if (bytes == null) {
      logger.warn(`[${project.name}] failed to fetch blob ${entry.blobHash} for ${entry.path}`)
      skipped += 1
      continue
    }

    const text = decodeUtf8(bytes)
    if (text == null) {
      skipped += 1
      continue
    }

    pendingUpserts.push({
      project: project.name,
      path: entry.path,
      blob_hash: entry.blobHash,
      size_bytes: entry.sizeBytes,
      language: detectLanguage(entry.path),
      content: text,
      indexed_at: new Date().toISOString()
    })

    if (existingHash === undefined) inserted += 1
    else updated += 1
    indexed.delete(entry.path)
  }

  // Anything still in `indexed` was either absent from the current tree, or
  // present in the tree but skipped this round (newly excluded, too big,
  // failed to fetch, not UTF-8). Schedule them for removal so the index
  // reflects what should currently be searchable.
  const pendingDeletes = Array.from(indexed.keys())
  const deleted = pendingDeletes.length

  // Phase 2 (sync, atomic): a single transaction so an interrupted sync
  // either lands all writes for this project or none of them.
  const flush = db.transaction(() => {
    for (const row of pendingUpserts) upsert.run(row)
    for (const path of pendingDeletes) remove.run(project.name, path)
  })
  flush()

  const totalCount = (db
    .prepare('SELECT COUNT(*) AS n FROM files WHERE project = ?')
    .get(project.name) as { n: number }).n

  updateCodeSyncState(db, project.name, totalCount)

  logger.info(
    `[${project.name}] code sync: +${inserted} new, ~${updated} updated, ` +
    `-${deleted} removed, ${skipped} skipped; ${totalCount} total file(s) in database`
  )

  return { project: project.name, fileCount: totalCount }
}

async function resolveIndexRef (git: SimpleGit, project: ProjectConfig): Promise<string> {
  if (project.indexRef !== undefined && project.indexRef.length > 0) {
    return project.indexRef
  }
  const raw = await git.raw(['symbolic-ref', '--short', 'refs/remotes/origin/HEAD'])
  const ref = raw.trim()
  if (ref.length === 0) {
    throw new Error(
      `Could not resolve default branch for "${project.name}" — set indexRef explicitly in config`
    )
  }
  return ref
}

async function readTree (git: SimpleGit, ref: string): Promise<TreeEntry[]> {
  // `git ls-tree -r --long <ref>` output:
  //   <mode> SP <type> SP <hash> SP<spaces><size> TAB <path>
  // example:
  //   100644 blob abc123...    1234	src/foo.ts
  const raw = await git.raw(['ls-tree', '-r', '--long', ref])
  const entries: TreeEntry[] = []

  for (const line of raw.split('\n')) {
    if (line.length === 0) continue
    const tabIndex = line.indexOf('\t')
    if (tabIndex < 0) continue

    const meta = line.slice(0, tabIndex)
    const path = line.slice(tabIndex + 1)
    const parts = meta.split(/\s+/)
    if (parts.length < 4) continue
    const [, type, blobHash, sizeStr] = parts
    if (type !== 'blob' || blobHash === undefined || sizeStr === undefined) continue
    const sizeBytes = Number.parseInt(sizeStr, 10)
    if (!Number.isFinite(sizeBytes)) continue

    entries.push({ blobHash, sizeBytes, path })
  }

  return entries
}

function readIndexedHashes (db: Database.Database, projectName: string): Map<string, string> {
  const rows = db
    .prepare('SELECT path, blob_hash FROM files WHERE project = ?')
    .all(projectName) as Array<{ path: string, blob_hash: string }>
  const map = new Map<string, string>()
  for (const row of rows) map.set(row.path, row.blob_hash)
  return map
}

function makeExcludeMatcher (patterns: string[]): (path: string) => boolean {
  if (patterns.length === 0) return () => false
  const matchers = patterns.map((p) => picomatch(p, { dot: true }))
  return (path) => matchers.some((m) => m(path))
}

function preFilter (entry: TreeEntry, isExcluded: (path: string) => boolean): boolean {
  if (isExcluded(entry.path)) return true
  if (entry.sizeBytes > MAX_FILE_BYTES) return true
  if (matchesDenylist(entry.path)) return true
  return false
}

function matchesDenylist (path: string): boolean {
  const filename = path.split('/').pop() ?? path
  if (FILENAME_DENYLIST.has(filename)) return true

  // Two-segment extensions like `.min.js` or `.min.css`
  const lower = filename.toLowerCase()
  for (const ext of EXTENSION_DENYLIST) {
    if (lower.endsWith(ext)) return true
  }
  return false
}

function detectLanguage (path: string): string | null {
  const ext = extname(path).toLowerCase()
  return EXTENSION_LANGUAGE[ext] ?? null
}

function fetchBlob (cwd: string, blobHash: string): Promise<Buffer | null> {
  return new Promise((resolveFn) => {
    const proc = spawn('git', ['cat-file', '-p', blobHash], { cwd })
    const chunks: Buffer[] = []
    let stderr = ''
    proc.stdout.on('data', (chunk: Buffer) => chunks.push(chunk))
    proc.stderr.on('data', (chunk: Buffer) => { stderr += chunk.toString('utf8') })
    proc.on('close', (code) => {
      if (code === 0) resolveFn(Buffer.concat(chunks))
      else {
        logger.warn(`git cat-file -p ${blobHash} exited ${code ?? '?'}: ${stderr.trim()}`)
        resolveFn(null)
      }
    })
    proc.on('error', (err) => {
      logger.warn(`git cat-file -p ${blobHash} failed to spawn: ${err.message}`)
      resolveFn(null)
    })
  })
}

// Defense-in-depth: only feed git-format SHA-1 (40 hex) or SHA-256 (64 hex)
// hashes back to `git cat-file -p`. Output of `git ls-tree` should always
// match this, but a malformed/malicious tree shouldn't get a chance to slip
// arbitrary text into the argv slot.
function isValidBlobHash (hash: string): boolean {
  return /^[0-9a-f]{40}$|^[0-9a-f]{64}$/.test(hash)
}

function decodeUtf8 (bytes: Buffer): string | null {
  try {
    return new TextDecoder('utf-8', { fatal: true }).decode(bytes)
  } catch {
    return null
  }
}

function updateCodeSyncState (db: Database.Database, projectName: string, fileCount: number): void {
  db.prepare(`
    INSERT INTO sync_state (project, source, last_synced, commit_count, ticket_count, file_count)
    VALUES (@project, 'code', @last_synced, NULL, NULL, @file_count)
    ON CONFLICT(project, source) DO UPDATE SET
      last_synced = excluded.last_synced,
      file_count  = excluded.file_count
  `).run({
    project: projectName,
    last_synced: new Date().toISOString(),
    file_count: fileCount
  })
}
