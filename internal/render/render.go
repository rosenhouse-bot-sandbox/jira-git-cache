package render

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/snapshot"
)

const (
	issueTimeLayout    = "2006-01-02T15:04:05.000Z"
	renderedTimeLayout = "2006-01-02T15:04:05Z"
)

var (
	projectKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	oneLine    = strings.NewReplacer("\r\n", " ", "\n", " ")
	indexValue = strings.NewReplacer("\r\n", " ", "\n", " ", "|", "/")
)

// Tree renders a snapshot into reader files, keyed by path relative to .jira-git-cache/.
// Projects in Meta.Projects that are not valid project keys are skipped.
func Tree(s snapshot.Snapshot) map[string][]byte {
	files := snapshot.Snapshot{Meta: s.Meta, Fields: s.Fields}.Encode()
	byProject := map[string][]snapshot.Issue{}
	for p := range s.Meta.Projects {
		if projectKey.MatchString(p) {
			byProject[p] = nil
		}
	}
	for _, iss := range s.Issues {
		p := snapshot.Project(iss.Key)
		byProject[p] = append(byProject[p], iss)
		files[p+"/"+iss.Key+"/issue.md"] = Issue(iss, s.Meta.JiraURL)
		files[p+"/"+iss.Key+"/issue.json"] = snapshot.EncodeIssue(iss)
	}
	for p, issues := range byProject {
		files[p+"/_index.md"] = Index(issues)
	}
	return files
}

// Issue renders an issue's issue.md.
func Issue(iss snapshot.Issue, jiraURL string) []byte {
	fields := []string{
		field("Status", iss.Status),
		field("Resolution", deref(iss.Resolution, "Unresolved")),
		field("Type", iss.Type),
		field("Priority", deref(iss.Priority, "")),
		field("Assignee", userName(iss.Assignee, "Unassigned")),
		field("Reporter", userName(iss.Reporter, "")),
		field("Labels", strings.Join(iss.Labels, ", ")),
		field("Parent", deref(iss.Parent, "")),
		field("Links", links(iss.Links)),
		field("Created", toSecond(iss.Created)),
		field("Updated", toSecond(iss.Updated)),
		field("URL", jiraURL+"/browse/"+iss.Key),
	}
	for _, name := range slices.Sorted(maps.Keys(iss.Custom)) {
		fields = append(fields, field(titleCase(name), customValue(iss.Custom[name])))
	}
	blocks := []string{"# " + oneLine.Replace(iss.Key+": "+iss.Summary), strings.Join(fields, "\n")}
	if d := markdown(deref(iss.Description, "")); d != "" {
		blocks = append(blocks, "## Description", d)
	}
	if len(iss.Comments) > 0 {
		blocks = append(blocks, "## Comments")
	}
	for _, c := range iss.Comments {
		author := oneLine.Replace(userName(c.Author, "Anonymous"))
		blocks = append(blocks, "### "+author+" — "+toSecond(c.Created))
		if b := markdown(c.Body); b != "" {
			blocks = append(blocks, b)
		}
	}
	return []byte(strings.Join(blocks, "\n\n") + "\n")
}

// Index renders a project's _index.md: one line per issue, in key order.
func Index(issues []snapshot.Issue) []byte {
	sorted := slices.Clone(issues)
	sort.Slice(sorted, func(i, j int) bool { return snapshot.LessKey(sorted[i].Key, sorted[j].Key) })
	var b strings.Builder
	for _, iss := range sorted {
		cols := []string{iss.Key, iss.Status, userName(iss.Assignee, "Unassigned"), iss.Type, iss.Summary}
		for i, c := range cols {
			cols[i] = indexValue.Replace(c)
		}
		b.WriteString(strings.Join(cols, " | ") + "\n")
	}
	return []byte(b.String())
}

func field(label, value string) string {
	return strings.TrimRight("- "+label+": "+oneLine.Replace(value), " ")
}

func markdown(wiki string) string {
	return strings.Trim(WikiToMarkdown(wiki), "\n")
}

func deref(s *string, none string) string {
	if s == nil {
		return none
	}
	return *s
}

func userName(u *snapshot.User, none string) string {
	if u == nil {
		return none
	}
	return cmp.Or(u.DisplayName, u.Name, none)
}

func links(ls []snapshot.Link) string {
	parts := make([]string, len(ls))
	for i, l := range ls {
		parts[i] = l.Relation + " " + l.Key
	}
	return strings.Join(parts, ", ")
}

func toSecond(issueTime string) string {
	t, err := time.Parse(issueTimeLayout, issueTime)
	if err != nil {
		return issueTime
	}
	return t.Format(renderedTimeLayout)
}

func titleCase(name string) string {
	words := strings.Split(name, "_")
	for i, w := range words {
		if r, n := utf8.DecodeRuneInString(w); n > 0 {
			words[i] = string(unicode.ToUpper(r)) + w[n:]
		}
	}
	return strings.Join(words, " ")
}

func customValue(v any) string {
	switch v := v.(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	case []any:
		parts := make([]string, len(v))
		for i, e := range v {
			parts[i] = customValue(e)
		}
		return strings.Join(parts, ", ")
	default:
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err != nil {
			return fmt.Sprint(v)
		}
		return strings.TrimSuffix(b.String(), "\n")
	}
}
