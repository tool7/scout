import Database from 'better-sqlite3'
import { mkdirSync, readFileSync, readdirSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { logger } from '../utils/logger.js'

type DB = Database.Database

const MIGRATIONS_DIR = resolve(dirname(fileURLToPath(import.meta.url)), 'migrations')

interface Migration {
  id: string
  sql: string
}

export function openDatabase (dbPath: string): DB {
  mkdirSync(dirname(dbPath), { recursive: true })
  const db = new Database(dbPath)
  db.pragma('journal_mode = WAL')
  db.pragma('foreign_keys = ON')
  applyMigrations(db)
  return db
}

function applyMigrations (db: DB): void {
  db.exec(`
    CREATE TABLE IF NOT EXISTS schema_migrations (
      id         TEXT PRIMARY KEY,
      applied_at TEXT NOT NULL
    );
  `)

  const applied = new Set(
    db.prepare<[], { id: string }>('SELECT id FROM schema_migrations')
      .all()
      .map((row) => row.id)
  )

  const migrations = loadMigrations()
  const pending = migrations.filter((migration) => !applied.has(migration.id))

  if (pending.length === 0) return

  const insertRecord = db.prepare(
    'INSERT INTO schema_migrations (id, applied_at) VALUES (?, ?)'
  )

  for (const migration of pending) {
    const run = db.transaction(() => {
      db.exec(migration.sql)
      insertRecord.run(migration.id, new Date().toISOString())
    })
    run()
    logger.info(`applied migration ${migration.id}`)
  }
}

function loadMigrations (): Migration[] {
  const files = readdirSync(MIGRATIONS_DIR)
    .filter((name) => name.endsWith('.sql'))
    .sort()

  return files.map((file) => ({
    id: file.replace(/\.sql$/, ''),
    sql: readFileSync(resolve(MIGRATIONS_DIR, file), 'utf8')
  }))
}
