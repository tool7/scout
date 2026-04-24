import { cosmiconfig } from 'cosmiconfig'
import { existsSync, readFileSync } from 'node:fs'
import { homedir } from 'node:os'
import { isAbsolute, resolve } from 'node:path'
import { configSchema, type Config } from './schema.js'

const MODULE_NAME = 'readcube-scout'
const HOME_CONFIG_PATH = resolve(homedir(), '.readcube-scout', 'config.json')

function expandHome (path: string): string {
  if (path === '~') return homedir()
  if (path.startsWith('~/')) return resolve(homedir(), path.slice(2))
  return path
}

function resolveFromBase (path: string, baseDir: string): string {
  const expanded = expandHome(path)
  return isAbsolute(expanded) ? expanded : resolve(baseDir, expanded)
}

async function findConfigSource (): Promise<{ config: unknown, filepath: string }> {
  const explorer = cosmiconfig(MODULE_NAME, {
    searchPlaces: [
      'readcube-scout.config.json',
      '.readcube-scout.json',
      `.config/${MODULE_NAME}/config.json`
    ]
  })

  const result = await explorer.search()
  if (result) return { config: result.config, filepath: result.filepath }

  if (existsSync(HOME_CONFIG_PATH)) {
    const raw = readFileSync(HOME_CONFIG_PATH, 'utf8')
    try {
      return { config: JSON.parse(raw), filepath: HOME_CONFIG_PATH }
    } catch (err) {
      throw new Error(
        `Failed to parse config at ${HOME_CONFIG_PATH}: ${(err as Error).message}`
      )
    }
  }

  throw new Error(
    'Configuration not found. Create readcube-scout.config.json in the project ' +
    `or a config file at ${HOME_CONFIG_PATH}. See readcube-scout.config.example.json for the expected shape.`
  )
}

export async function loadConfig (): Promise<Config> {
  const { config: raw, filepath } = await findConfigSource()
  const parsed = configSchema.safeParse(raw)

  if (!parsed.success) {
    const issues = parsed.error.issues
      .map((issue) => `  - ${issue.path.join('.') || '(root)'}: ${issue.message}`)
      .join('\n')
    throw new Error(`Invalid configuration at ${filepath}:\n${issues}`)
  }

  const baseDir = resolve(filepath, '..')
  const config = parsed.data

  return {
    ...config,
    dataDir: resolveFromBase(config.dataDir, baseDir),
    projects: config.projects.map((project) => ({
      ...project,
      gitPath: resolveFromBase(project.gitPath, baseDir)
    }))
  }
}
