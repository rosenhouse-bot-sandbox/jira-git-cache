package render_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/render"
)

func TestWikiToMarkdown(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"plain", "plain", "plain\n"},
		{"crlf", "a\r\nb\r\n", "a\nb\n"},
		{"blank line kept", "a\n\nb", "a\n\nb\n"},

		{"h1", "h1. T", "### T\n"},
		{"h2", "h2. T", "#### T\n"},
		{"h3", "h3. T", "##### T\n"},
		{"h4 caps at six", "h4. T", "###### T\n"},
		{"h6", "h6. T", "###### T\n"},
		{"h7 is text", "h7. T", "h7. T\n"},
		{"heading needs space", "h2.T", "h2.T\n"},
		{"heading text is converted", "h2. *T*", "#### **T**\n"},

		{"bq", "bq. q *b*", "> q **b**\n"},

		{"bullets", "* a\n** b\n- c\n*** d", "- a\n  - b\n- c\n    - d\n"},
		{"numbers", "# a\n## b", "1. a\n   1. b\n"},
		{"last marker sets type", "#* a\n*# b", "  - a\n   1. b\n"},
		{"list item is converted", "* {{x}} *y*", "- `x` **y**\n"},
		{"list needs space", "*a", "*a\n"},

		{"hr", "----", "---\n"},
		{"five dashes", "-----", "-----\n"},

		{"bold", "a *b* c", "a **b** c\n"},
		{"bold at line start", "*bold* text", "**bold** text\n"},
		{"bold with spaces inside", "*a b*", "**a b**\n"},
		{"two bolds", "*a* *b*", "**a** **b**\n"},
		{"bold in punctuation", "(*a*).", "(**a**).\n"},
		{"bold after word", "2*3*", "2*3*\n"},
		{"bold before word", "*3*4", "*3*4\n"},
		{"bold after underscore", "_*a*", "_*a*\n"},
		{"bold before unicode letter", "*a*é", "*a*é\n"},
		{"bold opening before space", "x * a* b", "x * a* b\n"},
		{"bold closing after space", "x *a * b", "x *a * b\n"},

		{"monospace", "{{x*y*}} *z*", "`x*y*` **z**\n"},
		{"inline code", "run {code}k *x*{code} now", "run `k *x*` now\n"},
		{"inline code with params", "{code:go}x{code}", "`x`\n"},
		{"inline noformat", "{noformat}*n*{noformat}", "`*n*`\n"},

		{"link", "see [the docs|https://x.example/a] now", "see [the docs](https://x.example/a) now\n"},
		{"bare https", "[https://x.example/a]", "<https://x.example/a>\n"},
		{"bare http", "[http://x.example]", "<http://x.example>\n"},
		{"mention", "ping [~jdoe]", "ping @jdoe\n"},
		{"plain brackets", "[PLAT-1]", "[PLAT-1]\n"},

		{"table", "||h1||h2||\n|a|b|", "||h1||h2||\n|a|b|\n"},

		{"code block", "{code}\n*x*\nh1. y\n{code}", "```\n*x*\nh1. y\n```\n"},
		{"code block lang", "{code:java|title=X}\nx\n{code}", "```java\nx\n```\n"},
		{"code block lang after title", "{code:title=X|go}\nx\n{code}", "```go\nx\n```\n"},
		{"code block no lang", "{code:title=X}\nx\n{code}", "```\nx\n```\n"},
		{"code block tags trimmed", "  {code}  \nx\n {code}", "```\nx\n```\n"},
		{"code block keeps crlf content", "{code}\r\n  a  \r\n{code}", "```\n  a  \n```\n"},
		{"noformat block", "{noformat}\n{code}\n{noformat}", "```\n{code}\n```\n"},
		{"code block ignores noformat", "{code}\n{noformat}\n{code}", "```\n{noformat}\n```\n"},

		{"quote", "{quote}\nh1. a\n* b\n\n{quote}\nafter", "\n> ### a\n> - b\n> \n\nafter\n"},
		{"code in quote", "{quote}\n{code}\n*x*\n{code}\n{quote}", "\n> ```\n> *x*\n> ```\n\n"},
		{"quote in code", "{code}\n{quote}\n{code}", "```\n{quote}\n```\n"},

		{"unclosed code", "a *b*\r\n{code}\r\n*x*", "a *b*\n{code}\n*x*\n"},
		{"unclosed noformat", "{noformat}\nh1. x", "{noformat}\nh1. x\n"},
		{"unclosed quote", "{quote}\n*x*", "{quote}\n*x*\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := render.WikiToMarkdown(c.in); got != c.want {
				t.Errorf("WikiToMarkdown(%q)\n got %q\nwant %q", c.in, got, c.want)
			}
		})
	}
}

func TestWikiToMarkdownGoldens(t *testing.T) {
	inputs, err := filepath.Glob("testdata/*.wiki")
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) == 0 {
		t.Fatal("no goldens")
	}
	for _, in := range inputs {
		t.Run(filepath.Base(in), func(t *testing.T) {
			wiki := readFile(t, in)
			want := readFile(t, strings.TrimSuffix(in, ".wiki")+".md")
			if got := render.WikiToMarkdown(wiki); got != want {
				t.Errorf("got:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
