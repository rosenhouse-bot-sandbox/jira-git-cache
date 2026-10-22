package render_test

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/jira"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/jiramock"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/render"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/snapshot"
)

const jiraURL = "https://jira.example.com"

func ptr[T any](v T) *T { return &v }

func exampleIssue(t *testing.T) snapshot.Issue {
	t.Helper()
	m := jiramock.ExampleIssue()
	var iss jira.Issue
	if err := json.Unmarshal(m.JSON(), &iss); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(m.Comments)
	if err != nil {
		t.Fatal(err)
	}
	var comments []jira.Comment
	if err := json.Unmarshal(raw, &comments); err != nil {
		t.Fatal(err)
	}
	out, err := snapshot.FromJira(iss, comments, map[string]string{
		"customfield_10002": "story_points",
		"customfield_10100": "sprint",
		"customfield_10007": "epic_link",
		"customfield_10500": "team",
		"customfield_10600": "reviewers",
		"customfield_10700": "parent_link",
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func minimalIssue(key string) snapshot.Issue {
	return snapshot.Issue{
		ID: "1", Key: key, Summary: "S", Status: "Open", Type: "Task",
		Created: "2026-10-01T14:02:11.999Z", Updated: "2026-10-20T09:15:40.000Z",
	}
}

const minimalHeader = `# INFRA-7: S

- Status: Open
- Resolution: Unresolved
- Type: Task
- Priority:
- Assignee: Unassigned
- Reporter:
- Labels:
- Parent:
- Links:
- Created: 2026-10-01T14:02:11Z
- Updated: 2026-10-20T09:15:40Z
- URL: https://jira.example.com/browse/INFRA-7
`

func TestIssueExample(t *testing.T) {
	got := string(render.Issue(exampleIssue(t), jiraURL))
	if want := readFile(t, "testdata/PLAT-123.issue.md"); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestIssueMinimal(t *testing.T) {
	if got := string(render.Issue(minimalIssue("INFRA-7"), jiraURL)); got != minimalHeader {
		t.Errorf("got:\n%s\nwant:\n%s", got, minimalHeader)
	}
}

func TestIssueOmitsBlankDescription(t *testing.T) {
	for i, d := range []*string{nil, ptr(""), ptr("\r\n\n"), ptr(" \n\t ")} {
		iss := minimalIssue("INFRA-7")
		iss.Description = d
		if got := string(render.Issue(iss, jiraURL)); got != minimalHeader {
			t.Errorf("case %d: got:\n%s", i, got)
		}
	}
}

func TestIssueTrimsBlankLinesAroundSections(t *testing.T) {
	iss := minimalIssue("INFRA-7")
	iss.Description = ptr(" \n\t\nfirst\n\nlast  \n \n")
	iss.Comments = []snapshot.Comment{{ID: "1", Created: "2026-10-19T17:30:02.000Z", Body: "\nc\n\n"}}
	want := minimalHeader + "\n## Description\n\nfirst\n\nlast\n\n## Comments\n\n### Anonymous — 2026-10-19T17:30:02Z\n\nc\n"
	if got := string(render.Issue(iss, jiraURL)); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestIssueComments(t *testing.T) {
	iss := minimalIssue("INFRA-7")
	iss.Comments = []snapshot.Comment{
		{ID: "1", Author: &snapshot.User{Name: "bob"}, Created: "2026-10-19T17:30:02.500Z", Body: "*hi*"},
		{ID: "2", Author: &snapshot.User{Name: "jdoe", DisplayName: "Jane\nDoe"}, Created: "bad time", Body: " \n "},
		{ID: "3", Created: "2026-10-20T08:00:00.000Z", Body: "x"},
	}
	want := minimalHeader + `
## Comments

### bob — 2026-10-19T17:30:02Z

**hi**

### Jane Doe — bad time

### Anonymous — 2026-10-20T08:00:00Z

x
`
	if got := string(render.Issue(iss, jiraURL)); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestIssueBodyLinesNeverLookLikeFields(t *testing.T) {
	iss := minimalIssue("INFRA-7")
	iss.Description = ptr("* Assignee: Jane\n- Sprint: Sprint 42\n{code}\n- Status: Done\n{code}")
	iss.Comments = []snapshot.Comment{
		{ID: "1", Created: "2026-10-19T17:30:02.000Z", Body: "-- Assignee: Jane"},
		{ID: "2", Created: "2026-10-19T17:30:02.000Z", Body: "- Assignee: Jane\n{code}"},
	}
	got := string(render.Issue(iss, jiraURL))
	if fields := strings.Count(got, "\n- "); fields != strings.Count(minimalHeader, "\n- ") {
		t.Errorf("%d lines start with \"- \":\n%s", fields, got)
	}
}

func TestIssueSystemFields(t *testing.T) {
	iss := minimalIssue("INFRA-7")
	iss.Summary = "two\r\nlines"
	iss.Resolution = ptr("Done")
	iss.Assignee = &snapshot.User{Name: "bob"}
	iss.Reporter = &snapshot.User{Name: "amy", DisplayName: "Amy"}
	iss.Labels = []string{"x"}
	iss.Links = []snapshot.Link{{Relation: "blocks", Key: "INFRA-8"}}
	iss.Created = "not a time"
	got := string(render.Issue(iss, jiraURL))
	for _, line := range []string{
		"# INFRA-7: two lines\n",
		"\n- Resolution: Done\n",
		"\n- Assignee: bob\n",
		"\n- Reporter: Amy\n",
		"\n- Labels: x\n",
		"\n- Links: blocks INFRA-8\n",
		"\n- Created: not a time\n",
	} {
		if !strings.Contains(got, line) {
			t.Errorf("missing %q in:\n%s", line, got)
		}
	}
}

func TestIssueCustomFields(t *testing.T) {
	iss := minimalIssue("INFRA-7")
	iss.Custom = map[string]any{
		"text":         "a\r\nb\nc",
		"story_points": 0.5,
		"big":          1e21,
		"flag":         false,
		"list":         []any{"x", 2.0, []any{"y", true}, map[string]any{"a": 1.0}, "z"},
		"empty_list":   []any{},
		"object":       map[string]any{"b": "<1>", "a": 1.0},
		"unset":        nil,
		"trailing_":    "t",
	}
	want := minimalHeader + `- Big: 1000000000000000000000
- Empty List:
- Flag: false
- List: x, 2, y, true, {"a":1}, z
- Object: {"a":1,"b":"<1>"}
- Story Points: 0.5
- Text: a b c
- Trailing : t
- Unset:
`
	if got := string(render.Issue(iss, jiraURL)); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestIndex(t *testing.T) {
	issues := []snapshot.Issue{
		{Key: "PLAT-10", Status: "Done", Type: "Bug", Summary: "a|b\r\nc", Assignee: &snapshot.User{Name: "jdoe", DisplayName: "Jane Doe"}},
		{Key: "PLAT-2", Status: "In | Progress", Type: "Task", Summary: "x\ny", Assignee: &snapshot.User{Name: "bob"}},
		{Key: "PLAT-3", Status: "Open", Type: "Sub\ntask", Summary: "s"},
	}
	want := "PLAT-2 | In / Progress | bob | Task | x y\n" +
		"PLAT-3 | Open | Unassigned | Sub task | s\n" +
		"PLAT-10 | Done | Jane Doe | Bug | a/b c\n"
	if got := string(render.Index(issues)); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if issues[0].Key != "PLAT-10" {
		t.Error("Index reordered its argument")
	}
}

func TestIndexEmpty(t *testing.T) {
	if got := render.Index(nil); len(got) != 0 {
		t.Errorf("got %q", got)
	}
}

func TestTree(t *testing.T) {
	fetched := time.Date(2026, 10, 22, 5, 0, 41, 0, time.UTC)
	plat1, plat2, other := minimalIssue("PLAT-1"), minimalIssue("PLAT-2"), minimalIssue("OTHER-3")
	s := snapshot.Snapshot{
		Meta: snapshot.Meta{
			FetchedAt: &fetched,
			JiraURL:   jiraURL,
			Projects: map[string]snapshot.ProjectMeta{
				"PLAT":  {JQL: "q", Count: 2},
				"EMPTY": {JQL: "q", Count: 0},
				"../X":  {JQL: "q", Count: 0},
				"X/..":  {JQL: "q", Count: 0},
			},
			SchemaVersion: snapshot.SchemaVersion,
		},
		Fields: []snapshot.Field{{ID: "customfield_1", Name: "One"}},
		Issues: map[string]snapshot.Issue{"PLAT-1": plat1, "PLAT-2": plat2, "OTHER-3": other},
	}
	got := render.Tree(s)

	encoded := s.Encode()
	want := map[string][]byte{
		"_meta/snapshot.json":      encoded["_meta/snapshot.json"],
		"_meta/fields.json":        encoded["_meta/fields.json"],
		"EMPTY/_index.md":          {},
		"PLAT/_index.md":           render.Index([]snapshot.Issue{plat1, plat2}),
		"PLAT/PLAT-1/issue.md":     render.Issue(plat1, jiraURL),
		"PLAT/PLAT-1/issue.json":   snapshot.EncodeIssue(plat1),
		"PLAT/PLAT-2/issue.md":     render.Issue(plat2, jiraURL),
		"PLAT/PLAT-2/issue.json":   snapshot.EncodeIssue(plat2),
		"OTHER/_index.md":          render.Index([]snapshot.Issue{other}),
		"OTHER/OTHER-3/issue.md":   render.Issue(other, jiraURL),
		"OTHER/OTHER-3/issue.json": snapshot.EncodeIssue(other),
	}
	if g, w := slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)); !slices.Equal(g, w) {
		t.Fatalf("paths:\n got %q\nwant %q", g, w)
	}
	for path, w := range want {
		if string(got[path]) != string(w) {
			t.Errorf("%s:\n got %q\nwant %q", path, got[path], w)
		}
	}
}

func TestTreeWithoutFields(t *testing.T) {
	got := render.Tree(snapshot.Snapshot{Meta: snapshot.Meta{SchemaVersion: snapshot.SchemaVersion}})
	if paths := slices.Collect(maps.Keys(got)); !slices.Equal(paths, []string{"_meta/snapshot.json"}) {
		t.Errorf("paths %q", paths)
	}
}
