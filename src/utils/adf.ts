// ADF = Atlassian Document Format: the JSON tree format Jira REST API v3
// uses for rich text (issue descriptions, comments). This module walks that
// tree and flattens it to plain text for FTS indexing: extracts `text` nodes,
// unwraps `mention` / `emoji` to readable names, turns `hardBreak` into \n,
// and appends a newline after block-level nodes (paragraph, heading,
// listItem, etc.). `adfToPlain` additionally collapses runs of 3+ newlines
// and trims the result.

const BLOCK_TYPES = new Set([
  'paragraph',
  'heading',
  'listItem',
  'bulletList',
  'orderedList',
  'blockquote',
  'codeBlock',
  'rule',
  'taskItem',
  'mediaSingle',
  'panel'
])

export function adfToText (node: unknown): string {
  if (node == null) return ''
  if (typeof node === 'string') return node
  if (typeof node !== 'object') return ''

  const record = node as Record<string, unknown>

  if (record.type === 'text' && typeof record.text === 'string') {
    return record.text
  }
  if (record.type === 'hardBreak') {
    return '\n'
  }
  if (record.type === 'mention' && record.attrs != null && typeof record.attrs === 'object') {
    const attrs = record.attrs as Record<string, unknown>
    if (typeof attrs.text === 'string') return attrs.text
    if (typeof attrs.displayName === 'string') return attrs.displayName
  }
  if (record.type === 'emoji' && record.attrs != null && typeof record.attrs === 'object') {
    const attrs = record.attrs as Record<string, unknown>
    if (typeof attrs.shortName === 'string') return attrs.shortName
  }

  let text = ''
  if (Array.isArray(record.content)) {
    text = record.content.map(adfToText).join('')
  }

  if (typeof record.type === 'string' && BLOCK_TYPES.has(record.type)) {
    text += '\n'
  }

  return text
}

export function adfToPlain (node: unknown): string {
  return adfToText(node)
    .replace(/\n{3,}/g, '\n\n')
    .trim()
}
