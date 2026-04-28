package adf

import (
	"regexp"
	"strings"
)

// ADF = Atlassian Document Format: the JSON tree format Jira REST API v3 uses
// for rich text (issue descriptions, comments). This package walks that tree
// and flattens it to plain text for FTS indexing: extracts `text` nodes,
// unwraps `mention` / `emoji` to readable names, turns `hardBreak` into \n,
// and appends a newline after block-level nodes (paragraph, heading, listItem,
// etc.). ToPlain additionally collapses runs of 3+ newlines and trims the
// result.

var blockTypes = map[string]struct{}{
	"paragraph":   {},
	"heading":     {},
	"listItem":    {},
	"bulletList":  {},
	"orderedList": {},
	"blockquote":  {},
	"codeBlock":   {},
	"rule":        {},
	"taskItem":    {},
	"mediaSingle": {},
	"panel":       {},
}

var collapseNewlines = regexp.MustCompile(`\n{3,}`)

func ToText(node any) string {
	if node == nil {
		return ""
	}
	if s, ok := node.(string); ok {
		return s
	}
	record, ok := node.(map[string]any)
	if !ok {
		return ""
	}

	nodeType, _ := record["type"].(string)

	if nodeType == "text" {
		if text, ok := record["text"].(string); ok {
			return text
		}
		return ""
	}
	if nodeType == "hardBreak" {
		return "\n"
	}
	if nodeType == "mention" {
		if attrs, ok := record["attrs"].(map[string]any); ok {
			if text, ok := attrs["text"].(string); ok {
				return text
			}
			if name, ok := attrs["displayName"].(string); ok {
				return name
			}
		}
	}
	if nodeType == "emoji" {
		if attrs, ok := record["attrs"].(map[string]any); ok {
			if shortName, ok := attrs["shortName"].(string); ok {
				return shortName
			}
		}
	}

	var b strings.Builder
	if content, ok := record["content"].([]any); ok {
		for _, child := range content {
			b.WriteString(ToText(child))
		}
	}

	if _, isBlock := blockTypes[nodeType]; isBlock {
		b.WriteByte('\n')
	}

	return b.String()
}

func ToPlain(node any) string {
	text := ToText(node)
	text = collapseNewlines.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}
