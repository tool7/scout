package fts

import (
	"regexp"
	"strings"
)

const termMinLength = 2

type Mode int

const (
	ModeNatural Mode = iota
	ModeCode
)

var (
	tokenPattern   = regexp.MustCompile(`"([^"]+)"|([^\s"]+)`)
	nonWordPattern = regexp.MustCompile(`[^\pL\pN]+`)
)

// ToQuery turns a raw user query (e.g. `why does "citation engine" fail`) into
// a safe FTS5 MATCH expression. Handles three cases in one pass:
//   - "quoted phrases"  -> phrase match, non-word chars collapsed to spaces
//   - bare words        -> split on non-letter/digit boundaries, drop tokens
//     shorter than 2 chars; in natural mode each token gets a `*` prefix-match
//     suffix; in code mode it stays bare (the trigram tokenizer used for code
//     FTS doesn't support prefix queries — substring matching is built in by
//     construction).
//   - punctuation, FTS5-special chars, stray quotes -> dropped silently
//
// Terms are joined with OR so BM25 can rank by combined match quality.
//
// Returns an empty string if nothing usable remains — callers should treat
// that as "no query" and bail before hitting MATCH (which errors on empty).
func ToQuery(raw string, mode Mode) string {
	var terms []string

	for _, match := range tokenPattern.FindAllStringSubmatch(raw, -1) {
		phraseRaw := match[1]
		wordRaw := match[2]

		if phraseRaw != "" {
			cleaned := strings.Join(splitOnNonWord(phraseRaw), " ")
			if cleaned != "" {
				terms = append(terms, `"`+cleaned+`"`)
			}
		} else if wordRaw != "" {
			for _, token := range splitOnNonWord(wordRaw) {
				if len(token) >= termMinLength {
					if mode == ModeCode {
						terms = append(terms, token)
					} else {
						terms = append(terms, token+"*")
					}
				}
			}
		}
	}

	if len(terms) == 0 {
		return ""
	}
	return strings.Join(terms, " OR ")
}

func splitOnNonWord(input string) []string {
	parts := nonWordPattern.Split(input, -1)
	out := parts[:0]
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
