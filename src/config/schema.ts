import { z } from 'zod'

const projectSchema = z.object({
  name: z.string().min(1, 'project name is required'),
  gitPath: z.string().min(1, 'gitPath is required'),
  jiraProjectKey: z.string().min(1, 'jiraProjectKey is required'),
  gitRemote: z.string().min(1).default('origin')
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
