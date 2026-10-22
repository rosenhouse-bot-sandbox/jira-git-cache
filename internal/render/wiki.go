// Package render turns a snapshot into the files readers grep.
package render

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	blockTag  = regexp.MustCompile(`^\{(code|noformat)(?::([^}]*))?\}$`)
	heading   = regexp.MustCompile(`^h([1-6])\. (.*)$`)
	listItem  = regexp.MustCompile(`^([*#-]+) (.*)$`)
	codeSpan  = regexp.MustCompile(`\{\{(.+?)\}\}|\{code(?::[^}]*)?\}(.*?)\{code\}|\{noformat\}(.*?)\{noformat\}`)
	namedLink = regexp.MustCompile(`\[([^\]|]+)\|([^\]]+)\]`)
	bareLink  = regexp.MustCompile(`\[(https?://[^\]|]+)\]`)
	mention   = regexp.MustCompile(`\[~([^\]]+)\]`)
	boldSpan  = regexp.MustCompile(`\*[^*\s](?:[^*]*[^*\s])?\*`)
)

// WikiToMarkdown converts Jira wiki markup to Markdown, line by line.
// If a {code}, {noformat} or {quote} block is left open, it returns the input
// with only its line endings normalized.
func WikiToMarkdown(wiki string) string {
	var lines []string
	for line := range strings.Lines(strings.ReplaceAll(wiki, "\r\n", "\n")) {
		lines = append(lines, strings.TrimSuffix(line, "\n"))
	}
	var out strings.Builder
	fence, quoted := "", false
	for _, line := range lines {
		tag := strings.TrimSpace(line)
		if fence == "" && tag == "{quote}" {
			quoted = !quoted
			out.WriteString("\n")
			continue
		}
		md := line
		if fence != "" {
			if tag == fence {
				fence, md = "", "```"
			}
		} else if m := blockTag.FindStringSubmatch(tag); m != nil {
			fence, md = "{"+m[1]+"}", "```"+language(m[2])
		} else {
			md = convertLine(line)
		}
		if quoted {
			md = "> " + md
		}
		out.WriteString(md + "\n")
	}
	if fence != "" || quoted {
		return strings.Join(lines, "\n") + "\n"
	}
	return out.String()
}

func language(params string) string {
	for p := range strings.SplitSeq(params, "|") {
		if !strings.Contains(p, "=") {
			return strings.TrimSpace(p)
		}
	}
	return ""
}

func convertLine(s string) string {
	if s == "----" {
		return "---"
	}
	if m := heading.FindStringSubmatch(s); m != nil {
		level := int(m[1][0]-'0') + 2
		return strings.Repeat("#", min(level, 6)) + " " + inline(m[2])
	}
	if t, ok := strings.CutPrefix(s, "bq. "); ok {
		return "> " + inline(t)
	}
	if m := listItem.FindStringSubmatch(s); m != nil {
		depth := len(m[1]) - 1
		if strings.HasSuffix(m[1], "#") {
			return strings.Repeat("   ", depth) + "1. " + inline(m[2])
		}
		return strings.Repeat("  ", depth) + "- " + inline(m[2])
	}
	return inline(s)
}

// inline converts text outside code spans and turns code spans into backticks.
func inline(s string) string {
	texts := codeSpan.Split(s, -1)
	out := text(texts[0])
	for i, m := range codeSpan.FindAllStringSubmatch(s, -1) {
		out += "`" + m[1] + m[2] + m[3] + "`" + text(texts[i+1])
	}
	return out
}

func text(s string) string {
	s = namedLink.ReplaceAllString(s, "[$1]($2)")
	s = bareLink.ReplaceAllString(s, "<$1>")
	s = mention.ReplaceAllString(s, "@$1")
	return bold(s)
}

// bold doubles the stars of *x* when they sit at a word's start and end.
func bold(s string) string {
	var out strings.Builder
	last := 0
	for _, m := range boldSpan.FindAllStringIndex(s, -1) {
		before, _ := utf8.DecodeLastRuneInString(s[:m[0]])
		after, _ := utf8.DecodeRuneInString(s[m[1]:])
		if isWord(before) || isWord(after) {
			continue
		}
		out.WriteString(s[last:m[0]] + "*" + s[m[0]:m[1]] + "*")
		last = m[1]
	}
	return out.String() + s[last:]
}

func isWord(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
