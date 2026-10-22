package snapshot_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/jira"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/jiramock"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/snapshot"
)

var updated = time.Date(2026, 10, 20, 11, 15, 40, 123e6, time.FixedZone("", 2*3600))

func fromMock(t *testing.T, m jiramock.Issue) (jira.Issue, []jira.Comment) {
	t.Helper()
	var iss jira.Issue
	if err := json.Unmarshal(m.JSON(), &iss); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(m.Comments)
	if err != nil {
		t.Fatal(err)
	}
	var comments []jira.Comment
	if err := json.Unmarshal(b, &comments); err != nil {
		t.Fatal(err)
	}
	return iss, comments
}

func ptr[T any](v T) *T { return &v }

func TestFromJiraExampleIssue(t *testing.T) {
	iss, comments := fromMock(t, jiramock.ExampleIssue())
	got, err := snapshot.FromJira(iss, comments, map[string]string{
		"customfield_10100": "sprint",
		"customfield_10002": "story_points",
		"customfield_10007": "epic_link",
		"customfield_10500": "team",
		"customfield_10600": "reviewers",
		"customfield_10700": "parent_link",
		"customfield_99999": "not_on_issue",
	})
	if err != nil {
		t.Fatal(err)
	}

	jdoe := &snapshot.User{Name: "jdoe", DisplayName: "Jane Doe"}
	jsmith := &snapshot.User{Name: "jsmith", DisplayName: "John Smith"}
	want := snapshot.Issue{
		ID:         "10230",
		Key:        "PLAT-123",
		Summary:    "Controller leaks watches on restart",
		Status:     "In Progress",
		Resolution: nil,
		Type:       "Bug",
		Priority:   ptr("High"),
		Assignee:   jdoe,
		Reporter:   jsmith,
		Labels:     []string{"controller", "k8s"},
		Parent:     ptr("PLAT-100"),
		Links: []snapshot.Link{
			{Relation: "blocks", Key: "PLAT-130"},
			{Relation: "relates to", Key: "INFRA-77"},
		},
		Created:     "2026-10-01T14:02:11.000Z",
		Updated:     "2026-10-20T09:15:40.123Z",
		Description: ptr("h2. Repro\r\n* restart controller\r\n{code}kubectl ...{code}"),
		Custom: map[string]any{
			"sprint":       []any{"Sprint 41", "Sprint 42"},
			"story_points": 3.0,
			"epic_link":    "PLAT-90",
			"team":         "Platform",
			"reviewers":    []any{"Jane Doe"},
			"parent_link":  nil,
			"not_on_issue": nil,
		},
		Comments: []snapshot.Comment{
			{ID: "40001", Author: jdoe, Created: "2026-10-19T17:30:02.000Z", Updated: "2026-10-19T17:30:02.000Z",
				Body: "Repro'd on *staging*.\r\n{code}\r\nkubectl get pods\r\n{code}"},
			{ID: "40002", Author: jsmith, Created: "2026-10-20T08:00:00.000Z", Updated: "2026-10-20T09:00:00.000Z",
				Body: "Fixed."},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestFromJiraMinimalIssue(t *testing.T) {
	m := jiramock.NewIssue("PLAT-7", 10007, updated)
	m.Fields["resolution"] = map[string]any{"name": "Done"}
	delete(m.Fields, "labels")
	m.Fields["issuelinks"] = nil
	iss, _ := fromMock(t, m)

	got, err := snapshot.FromJira(iss, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	want := snapshot.Issue{
		ID:         "10007",
		Key:        "PLAT-7",
		Summary:    "Issue PLAT-7",
		Status:     "Open",
		Resolution: ptr("Done"),
		Type:       "Task",
		Priority:   ptr("Medium"),
		Reporter:   &snapshot.User{Name: "jsmith", DisplayName: "John Smith"},
		Labels:     []string{},
		Links:      []snapshot.Link{},
		Created:    "2026-10-20T09:15:40.123Z",
		Updated:    "2026-10-20T09:15:40.123Z",
		Custom:     map[string]any{},
		Comments:   []snapshot.Comment{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestFromJiraDropsLinkWithoutIssue(t *testing.T) {
	m := jiramock.NewIssue("PLAT-7", 10007, updated)
	m.Fields["issuelinks"] = []any{map[string]any{"type": map[string]any{"inward": "is blocked by", "outward": "blocks"}}}
	iss, _ := fromMock(t, m)

	got, err := snapshot.FromJira(iss, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Links) != 0 {
		t.Errorf("Links = %v, want none", got.Links)
	}
}

func TestFromJiraReadsInwardLinkRelation(t *testing.T) {
	m := jiramock.NewIssue("PLAT-7", 10007, updated)
	m.Fields["issuelinks"] = []any{map[string]any{
		"type":        map[string]any{"inward": "is blocked by", "outward": "blocks"},
		"inwardIssue": map[string]any{"key": "PLAT-1"},
	}}
	iss, _ := fromMock(t, m)

	got, err := snapshot.FromJira(iss, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []snapshot.Link{{Relation: "is blocked by", Key: "PLAT-1"}}; !reflect.DeepEqual(got.Links, want) {
		t.Errorf("Links = %v, want %v", got.Links, want)
	}
}

func TestFromJiraNamesMalformedSystemField(t *testing.T) {
	m := jiramock.NewIssue("PLAT-7", 10007, updated)
	m.Fields["issuelinks"] = map[string]any{}
	iss, _ := fromMock(t, m)

	_, err := snapshot.FromJira(iss, nil, nil)
	if err == nil || !strings.HasPrefix(err.Error(), "PLAT-7: issuelinks: ") || strings.Contains(err.Error(), "struct") {
		t.Errorf("err = %v", err)
	}
}

func TestFromJiraRejectsMissingOrMalformedRequiredFields(t *testing.T) {
	for _, tc := range []struct {
		field string
		value any
		want  string
	}{
		{"summary", nil, "summary"},
		{"summary", 5, "summary"},
		{"status", nil, "status.name"},
		{"status", map[string]any{}, "status.name"},
		{"status", "Open", "status"},
		{"issuetype", map[string]any{"name": nil}, "issuetype.name"},
		{"created", nil, "created"},
		{"created", "yesterday", "created"},
		{"updated", nil, "updated"},
		{"updated", "2026-10-20", "updated"},
	} {
		m := jiramock.NewIssue("PLAT-7", 10007, updated)
		m.Fields[tc.field] = tc.value
		iss, _ := fromMock(t, m)

		_, err := snapshot.FromJira(iss, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "PLAT-7") || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s = %#v: err = %v, want one naming PLAT-7 and %s", tc.field, tc.value, err, tc.want)
		}
	}
}

func TestFromJiraTreatsAbsentRequiredFieldAsMissing(t *testing.T) {
	m := jiramock.NewIssue("PLAT-7", 10007, updated)
	delete(m.Fields, "summary")
	iss, _ := fromMock(t, m)

	if _, err := snapshot.FromJira(iss, nil, nil); err == nil || !strings.Contains(err.Error(), "summary") {
		t.Errorf("err = %v", err)
	}
}

func TestFromJiraDedupesAndSortsComments(t *testing.T) {
	m := jiramock.NewIssue("PLAT-7", 10007, updated)
	m.Comments = []map[string]any{
		{"id": "10", "created": "2026-10-19T12:00:00.000+0200", "updated": "2026-10-19T12:00:00.000+0200", "body": "ten"},
		{"id": "9", "created": "2026-10-19T10:00:00.000+0000", "updated": "2026-10-19T10:00:00.000+0000", "body": "nine"},
		{"id": "11", "created": "2026-10-19T09:00:00.000+0000", "updated": "2026-10-19T09:00:00.000+0000", "body": "eleven"},
		{"id": "10", "created": "2026-10-18T00:00:00.000+0000", "updated": "2026-10-18T00:00:00.000+0000", "body": "duplicate"},
	}
	iss, comments := fromMock(t, m)

	got, err := snapshot.FromJira(iss, comments, nil)
	if err != nil {
		t.Fatal(err)
	}
	var bodies []string
	for _, c := range got.Comments {
		bodies = append(bodies, c.Body)
		if c.Author != nil {
			t.Errorf("comment %s: Author = %v, want nil", c.ID, c.Author)
		}
	}
	if want := []string{"eleven", "nine", "ten"}; !reflect.DeepEqual(bodies, want) {
		t.Errorf("bodies = %v, want %v", bodies, want)
	}
}

func TestFromJiraRejectsBadCommentTimes(t *testing.T) {
	for _, field := range []string{"created", "updated"} {
		c := map[string]any{"id": "40001", "created": "2026-10-19T12:00:00.000+0200", "updated": "2026-10-19T12:00:00.000+0200", "body": ""}
		c[field] = "soon"
		m := jiramock.NewIssue("PLAT-7", 10007, updated)
		m.Comments = []map[string]any{c}
		iss, comments := fromMock(t, m)

		_, err := snapshot.FromJira(iss, comments, nil)
		if err == nil || !strings.Contains(err.Error(), "PLAT-7") || !strings.Contains(err.Error(), "40001") {
			t.Errorf("bad %s: err = %v, want one naming PLAT-7 and 40001", field, err)
		}
	}
}

func TestFromJiraRejectsInvalidSystemJSON(t *testing.T) {
	iss, _ := fromMock(t, jiramock.NewIssue("PLAT-7", 10007, updated))
	iss.Fields["labels"] = json.RawMessage("[")

	if _, err := snapshot.FromJira(iss, nil, nil); err == nil || !strings.Contains(err.Error(), "PLAT-7") {
		t.Errorf("err = %v", err)
	}
}

func TestFromJiraRejectsInvalidCustomJSON(t *testing.T) {
	iss, _ := fromMock(t, jiramock.NewIssue("PLAT-7", 10007, updated))
	iss.Fields["customfield_1"] = json.RawMessage("{")

	_, err := snapshot.FromJira(iss, nil, map[string]string{"customfield_1": "x"})
	if err == nil || !strings.Contains(err.Error(), "customfield_1") {
		t.Errorf("err = %v", err)
	}
}

func customValue(t *testing.T, raw string) any {
	t.Helper()
	iss, _ := fromMock(t, jiramock.NewIssue("PLAT-7", 10007, updated))
	iss.Fields["customfield_1"] = json.RawMessage(raw)
	got, err := snapshot.FromJira(iss, nil, map[string]string{"customfield_1": "x"})
	if err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return got.Custom["x"]
}

func TestFromJiraNormalizesCustomValues(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want any
	}{
		{`null`, nil},
		{`"PLAT-90"`, "PLAT-90"},
		{`3.5`, 3.5},
		{`true`, true},
		{`{"self":"x","value":"Platform","id":"10401"}`, "Platform"},
		{`{"value":"Hardware","child":{"value":"Disk","id":"2"}}`, "Hardware > Disk"},
		{`{"value":"Hardware","child":{"id":"2"}}`, "Hardware"},
		{`{"name":"n","child":{"value":"c"}}`, "n"},
		{`{"displayName":"Jane Doe","child":{"value":"x"}}`, "Jane Doe"},
		{`{"displayName":"Jane Doe","name":"jdoe","key":"JIRAUSER1"}`, "Jane Doe"},
		{`{"name":"jdoe","key":"JIRAUSER1"}`, "jdoe"},
		{`{"key":"JIRAUSER1"}`, "JIRAUSER1"},
		{`{"value":7,"name":"seven"}`, "seven"},
		{`{"hasEpicLinkFieldDependency":false,"showField":false,"nonEditableReason":{"reason":"X"}}`, nil},
		{`{"showField":false,"value":null}`, nil},
		{`{"value":null}`, map[string]any{"value": nil}},
		{`{"showField":false,"name":5}`, map[string]any{"showField": false, "name": 5.0}},
		{`{"showField":true}`, map[string]any{"showField": true}},
		{`{"id":15279,"state":"closed"}`, map[string]any{"id": 15279.0, "state": "closed"}},
		{`[]`, []any{}},
		{`[{"value":"A"},null,"b",{"showField":false},2]`, []any{"A", "b", 2.0}},
		{`[[{"name":"n"}]]`, []any{[]any{"n"}}},
	} {
		if got := customValue(t, tc.raw); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %#v, want %#v", tc.raw, got, tc.want)
		}
	}
}

func TestFromJiraDropsUnmappedCustomFields(t *testing.T) {
	iss, _ := fromMock(t, jiramock.ExampleIssue())

	got, err := snapshot.FromJira(iss, nil, map[string]string{"customfield_10002": "story_points"})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"story_points": 3.0}; !reflect.DeepEqual(got.Custom, want) {
		t.Errorf("Custom = %v, want %v", got.Custom, want)
	}
}

func sprintName(t *testing.T, dump string) any {
	t.Helper()
	b, err := json.Marshal(dump)
	if err != nil {
		t.Fatal(err)
	}
	return customValue(t, string(b))
}

func TestFromJiraReadsSprintNames(t *testing.T) {
	for _, name := range []string{"Sprint 42", "Team A, Sprint 3", "Team | Sprint 3", "a=b,c"} {
		for _, goal := range []string{"", "Ship it", "line one\r\nline two,\nline three", "Fix x,name=Bogus", "a,id=1,name=Bogus,state=X",
			"a,id=1,incompleteIssuesDestinationId=2,name=Bogus", "a,autoStartStop=x,name=Bogus"} {
			for order, dump := range map[string]string{
				"8.x":  jiramock.Sprint8x(42, name, "ACTIVE", goal),
				"10.x": jiramock.Sprint10x(42, name, "ACTIVE", goal),
			} {
				if got := sprintName(t, dump); got != name {
					t.Errorf("%s name %q goal %q: got %q", order, name, goal, got)
				}
			}
		}
	}
}

func TestFromJiraSplitsSprintNameAtEveryKnownKey(t *testing.T) {
	for _, k := range []string{"activatedDate", "autoStartStop", "completeDate", "endDate", "goal", "id",
		"incompleteIssuesDestinationId", "name", "rapidViewId", "sequence", "startDate", "state", "synced"} {
		dump := "com.atlassian.greenhopper.service.sprint.Sprint@1a2b[name=Real," + k + "=x]"
		if got := sprintName(t, dump); got != "Real" {
			t.Errorf("%s: got %q", k, got)
		}
	}
}

func TestFromJiraReadsSprintNameAfterLeadingGoal(t *testing.T) {
	dump := "com.atlassian.greenhopper.service.sprint.Sprint@1a2b[goal=x,name=Bogus,name=Real]"
	if got := sprintName(t, dump); got != "Real" {
		t.Errorf("got %q", got)
	}
}

func TestFromJiraKeepsOtherSprintLikeStrings(t *testing.T) {
	for _, s := range []string{
		"com.atlassian.greenhopper.service.sprint.Sprint@1a2b[id=42,state=ACTIVE,goal=,name]",
		"com.atlassian.greenhopper.service.sprint.Sprint@1A2B[id=42,name=Sprint 42]",
		"com.atlassian.greenhopper.service.sprint.Sprint@1a2b[id=42,name=Sprint 42] ",
		"xcom.atlassian.greenhopper.service.sprint.Sprint@1a2b[id=42,name=Sprint 42]",
		"Sprint 42",
	} {
		if got := sprintName(t, s); got != s {
			t.Errorf("%q: got %q", s, got)
		}
	}
}
