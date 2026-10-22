// Package render turns a snapshot into the files readers grep.
package render

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	blockTag = regexp.MustCompile(`^\{(code|noformat)(?::([^}]*))?\}$`)
	heading  = regexp.MustCompile(`^h([1-6])\. (.*)$`)
	listItem = regexp.MustCompile(`^([*#-]+) (.*)$`)
	codeSpan = regexp.MustCompile(`\{\{(.+?)\}\}|\{code(?::[^}]*)?\}(.*?)\{code\}|\{noformat\}(.*?)\{noformat\}`)
	link     = regexp.MustCompile(`\[~([^\]|]+)\]|\[([^\]|]+)\|([^\]|]+)(?:\|[^\]]*)?\]|\[(https?://[^\]|]+)\]`)
	boldSpan = regexp.MustCompile(`\*[^*\s](?:[^*]*[^*\s])?\*`)
)

// WikiToMarkdown converts Jira wiki markup to Markdown, line by line.
// If a {code}, {noformat} or {quote} block is left open, it returns the input
// with only its line endings normalized and its "- " lines indented.
// No line it returns starts with "- ", so none looks like an issue.md field.
func WikiToMarkdown(wiki string) string {
	var lines []string
	for line := range strings.Lines(strings.ReplaceAll(wiki, "\r\n", "\n")) {
		lines = append(lines, strings.TrimSuffix(line, "\n"))
	}
	raw := strings.Join(indent(lines, isDashItem), "\n") + "\n"
	var out strings.Builder
	quoted := false
	for i := 0; i < len(lines); i++ {
		tag := strings.TrimSpace(lines[i])
		if tag == "{quote}" {
			quoted = !quoted
			out.WriteString("\n")
			continue
		}
		block := []string{convertLine(lines[i])}
		if m := blockTag.FindStringSubmatch(tag); m != nil {
			body := lines[i+1:]
			n := slices.IndexFunc(body, func(l string) bool { return strings.TrimSpace(l) == "{"+m[1]+"}" })
			if n < 0 {
				return raw
			}
			body = body[:n]
			f := fence(strings.Join(body, "\n"), 3)
			block = slices.Concat([]string{f + language(m[2])}, body, []string{f})
			if slices.ContainsFunc(body, isDashItem) {
				block = indent(block, func(string) bool { return true })
			}
			i += n + 1
		}
		for _, md := range block {
			if quoted {
				md = "> " + md
			}
			out.WriteString(md + "\n")
		}
	}
	if quoted {
		return raw
	}
	return out.String()
}

func isDashItem(line string) bool { return strings.HasPrefix(line, "- ") }

// indent prefixes the lines that match with one space. Markdown ignores it:
// list items and fences may be indented, and a fence's indent is stripped
// from its content.
func indent(lines []string, match func(string) bool) []string {
	out := slices.Clone(lines)
	for i, line := range out {
		if match(line) {
			out[i] = " " + line
		}
	}
	return out
}

// fence returns the shortest backtick run, at least minLen long, that s does not contain.
func fence(s string, minLen int) string {
	f := strings.Repeat("`", minLen)
	for strings.Contains(s, f) {
		f += "`"
	}
	return f
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
	if strings.TrimSpace(s) == "----" {
		return "***"
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
		return strings.Repeat("  ", depth) + "* " + inline(m[2])
	}
	return inline(s)
}

// inline converts text outside code spans and turns code spans into backticks.
func inline(s string) string {
	texts := codeSpan.Split(s, -1)
	out := text(texts[0])
	for i, m := range codeSpan.FindAllStringSubmatch(s, -1) {
		out += codeSpanOf(m[1]+m[2]+m[3]) + text(texts[i+1])
	}
	return out
}

func codeSpanOf(s string) string {
	f := fence(s, 1)
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		s = " " + s + " "
	}
	return f + s + f
}

func text(s string) string {
	return bold(link.ReplaceAllStringFunc(s, convertLink))
}

func convertLink(l string) string {
	m := link.FindStringSubmatch(l)
	if m[1] != "" {
		return "@" + m[1]
	}
	if m[4] != "" {
		return "<" + m[4] + ">"
	}
	return "[" + m[2] + "](" + m[3] + ")"
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
