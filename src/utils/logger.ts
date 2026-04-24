type Level = 'debug' | 'info' | 'warn' | 'error'

function write (level: Level, message: string): void {
  const timestamp = new Date().toISOString()
  process.stderr.write(`[${timestamp}] ${level.toUpperCase()} ${message}\n`)
}

export const logger = {
  debug: (message: string): void => { write('debug', message) },
  info: (message: string): void => { write('info', message) },
  warn: (message: string): void => { write('warn', message) },
  error: (message: string): void => { write('error', message) }
}
