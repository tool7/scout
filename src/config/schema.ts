import { z } from 'zod'

// Jira project keys are documented as uppercase letters, digits, and
// underscores, starting with a letter (e.g. NEWAPP, DES). We enforce
// that here both as a typo guard and to prevent JQL quote injection in
// `project = "<key>"` clauses.
const JIRA_PROJECT_KEY_PATTERN = /^[A-Z][A-Z0-9_]+$/

const projectSchema = z.object({
  name: z.string().min(1, 'project name is required'),
  gitPath: z.string().min(1, 'gitPath is required'),
  jiraProjectKey: z.string().regex(
    JIRA_PROJECT_KEY_PATTERN,
    'jiraProjectKey must match /^[A-Z][A-Z0-9_]+$/ (e.g. NEWAPP, DES)'
  ),
  gitRemote: z.string().min(1).default('origin'),
  indexRef: z.string().min(1).optional(),
  excludePaths: z.array(z.string().min(1)).default([])
})

const jiraSchema = z.object({
  host: z.string().url('jira.host must be a valid URL'),
  email: z.string().email('jira.email must be a valid email address'),
  apiToken: z.string().min(1, 'jira.apiToken is required')
})

export const configSchema = z.object({
  dataDir: z.string().min(1, 'dataDir is required'),
  jira: jiraSchema,
  projects: z.array(projectSchema).min(1, 'at least one project must be configured')
})

export type Config = z.infer<typeof configSchema>
export type ProjectConfig = z.infer<typeof projectSchema>
export type JiraConfig = z.infer<typeof jiraSchema>
