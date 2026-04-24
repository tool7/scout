const TERM_MIN_LENGTH = 2

// Turns a raw user query (e.g. `why does "citation engine" fail`) into a safe
// FTS5 MATCH expression. Handles three cases in one pass:
//   - "quoted phrases"  -> phrase match, non-word chars collapsed to spaces
//                          (e.g. "citation engine" -> "citation engine")
//   - bare words        -> split on non-letter/digit boundaries, drop tokens
//                          shorter than 2 chars, append `*` for prefix match
//                          (e.g. implementations -> implementations*)
//   - punctuation, FTS5-special chars, stray quotes -> dropped silently
//
// All resulting terms are joined with OR so BM25 can rank by combined match
// quality instead of requiring every token to appear (implicit AND was too
// strict for natural-language queries). The Porter stemmer on the FTS tables
// makes `implementations*` match `implement`, `implemented`, `implementing`.
//
// Returns an empty string if nothing usable remains — callers should treat
// that as "no query" and bail before hitting MATCH (which errors on empty).
export function toFtsQuery (raw: string): string {
  const terms: string[] = []
  const pattern = /"([^"]+)"|([^\s"]+)/g

  let match: RegExpExecArray | null
  while ((match = pattern.exec(raw)) !== null) {
    const phraseRaw = match[1]
    const wordRaw = match[2]

    if (phraseRaw !== undefined) {
      const cleaned = splitOnNonWord(phraseRaw).join(' ')
      if (cleaned.length > 0) terms.push(`"${cleaned}"`)
    } else if (wordRaw !== undefined) {
      for (const token of splitOnNonWord(wordRaw)) {
        if (token.length >= TERM_MIN_LENGTH) terms.push(`${token}*`)
      }
    }
  }

  if (terms.length === 0) return ''
  return terms.join(' OR ')
}

function splitOnNonWord (input: string): string[] {
  return input.split(/[^\p{L}\p{N}]+/gu).filter((token) => token.length > 0)
}
