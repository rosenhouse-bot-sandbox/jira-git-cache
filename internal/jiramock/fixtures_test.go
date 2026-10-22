package jiramock_test

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/jiramock"
)

func TestNewIssueFormatsTimesInUpdatedsLocation(t *testing.T) {
	updated := time.Date(2026, 10, 20, 9, 15, 40, 123e6, time.FixedZone("", -5*3600))

	got := jiramock.NewIssue("PLAT-7", 10007, updated)

	want := jiramock.Issue{ID: "10007", Key: "PLAT-7", Fields: map[string]any{
		"summary":     "Issue PLAT-7",
		"status":      map[string]any{"name": "Open", "statusCategory": map[string]any{"key": "new", "name": "To Do"}},
		"resolution":  nil,
		"issuetype":   map[string]any{"name": "Task", "subtask": false},
		"priority":    map[string]any{"name": "Medium"},
		"assignee":    nil,
		"reporter":    map[string]any{"name": "jsmith", "displayName": "John Smith", "active": true},
		"labels":      []any{},
		"issuelinks":  []any{},
		"created":     "2026-10-20T09:15:40.123-0500",
		"updated":     "2026-10-20T09:15:40.123-0500",
		"description": nil,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NewIssue = %#v\nwant %#v", got, want)
	}
}

func TestFixturesHoldDecodedJSONValues(t *testing.T) {
	for _, iss := range []jiramock.Issue{jiramock.NewIssue("PLAT-1", 1, jiramock.DefaultNow), jiramock.ExampleIssue()} {
		var body struct {
			Fields map[string]any `json:"fields"`
		}
		if err := json.Unmarshal(iss.JSON(), &body); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(body.Fields, iss.Fields) {
			t.Errorf("%s: fields do not survive a JSON round trip", iss.Key)
		}
		var comments []map[string]any
		if err := json.Unmarshal(mustMarshal(t, iss.Comments), &comments); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(comments, iss.Comments) {
			t.Errorf("%s: comments do not survive a JSON round trip", iss.Key)
		}
	}

	var fields []map[string]any
	if err := json.Unmarshal(mustMarshal(t, jiramock.ExampleFields()), &fields); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fields, jiramock.ExampleFields()) {
		t.Error("ExampleFields does not survive a JSON round trip")
	}
}

func TestIssueJSON(t *testing.T) {
	iss := jiramock.Issue{ID: "5", Key: "A-1", Fields: map[string]any{"summary": "s"}}

	got := string(iss.JSON())

	want := `{"expand":"renderedFields,names,schema,operations,editmeta,changelog,versionedRepresentations",` +
		`"id":"5","self":"https://jira.example.com/rest/api/2/issue/5","key":"A-1","fields":{"summary":"s"}}`
	if got != want {
		t.Errorf("JSON = %s\nwant %s", got, want)
	}
}

func TestIssueJSONPanicsOnUnencodableField(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("no panic")
		}
	}()
	jiramock.Issue{Fields: map[string]any{"x": make(chan int)}}.JSON()
}

func TestExampleIssue(t *testing.T) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(jiramock.ExampleIssue()); err != nil {
		t.Fatal(err)
	}

	want, err := os.ReadFile("testdata/example_issue.json")
	if err != nil {
		t.Fatal(err)
	}
	if buf.String() != string(want) {
		t.Errorf("ExampleIssue differs from testdata/example_issue.json:\n%s", buf.String())
	}
}

func TestExampleIssueReturnsFreshCopies(t *testing.T) {
	a := jiramock.ExampleIssue()
	a.Fields["assignee"].(map[string]any)["name"] = "changed"
	a.Comments[0]["author"].(map[string]any)["name"] = "changed"

	b := jiramock.ExampleIssue()

	if b.Fields["assignee"].(map[string]any)["name"] != "jdoe" {
		t.Error("assignee shared between calls")
	}
	if b.Comments[0]["author"].(map[string]any)["name"] != "jdoe" {
		t.Error("comment author shared with assignee or between calls")
	}
}

func TestExampleFields(t *testing.T) {
	byID := map[string]map[string]any{}
	for _, f := range jiramock.ExampleFields() {
		byID[f["id"].(string)] = f
	}
	for id, custom := range map[string]string{
		"customfield_10100": "com.pyxis.greenhopper.jira:gh-sprint",
		"customfield_10002": "com.atlassian.jira.plugin.system.customfieldtypes:float",
		"customfield_10007": "com.pyxis.greenhopper.jira:gh-epic-link",
		"customfield_10500": "com.atlassian.jira.plugin.system.customfieldtypes:select",
		"customfield_10600": "com.atlassian.jira.plugin.system.customfieldtypes:multiuserpicker",
		"customfield_10700": "com.atlassian.jpo:jpo-custom-field-parent",
	} {
		f := byID[id]
		schema, _ := f["schema"].(map[string]any)
		if f["custom"] != true || schema["custom"] != custom {
			t.Errorf("%s = %v", id, f)
		}
	}
	if byID["customfield_10002"]["name"] != "Story Points" {
		t.Errorf("story points = %v", byID["customfield_10002"])
	}
	wantSprint := map[string]any{"id": "customfield_10100", "name": "Sprint", "custom": true,
		"orderable": true, "navigable": true, "searchable": true,
		"clauseNames": []any{"cf[10100]", "Sprint"},
		"schema": map[string]any{"type": "array", "items": "string",
			"custom": "com.pyxis.greenhopper.jira:gh-sprint", "customId": float64(10100)}}
	if !reflect.DeepEqual(byID["customfield_10100"], wantSprint) {
		t.Errorf("sprint = %v", byID["customfield_10100"])
	}
	wantSummary := map[string]any{"id": "summary", "name": "Summary", "custom": false,
		"orderable": true, "navigable": true, "searchable": true,
		"clauseNames": []any{"summary"}, "schema": map[string]any{"type": "string", "system": "summary"}}
	if !reflect.DeepEqual(byID["summary"], wantSummary) {
		t.Errorf("summary = %v", byID["summary"])
	}
	for _, id := range []string{"status", "issuetype"} {
		if byID[id]["custom"] != false {
			t.Errorf("%s = %v", id, byID[id])
		}
	}
}

func TestSprintDumps(t *testing.T) {
	goal := "ship it,name=Fake\nline 2"

	got8 := jiramock.Sprint8x(7, "Team | Sprint 3, part 2", "CLOSED", goal)
	got10 := jiramock.Sprint10x(7, "Team | Sprint 3, part 2", "CLOSED", goal)

	want8 := "com.atlassian.greenhopper.service.sprint.Sprint@1a2b[id=7,rapidViewId=<null>,state=CLOSED," +
		"name=Team | Sprint 3, part 2,startDate=2026-10-06T09:00:00.000Z,endDate=2026-10-20T00:00:00.000Z," +
		"completeDate=<null>,sequence=7,goal=ship it,name=Fake\nline 2]"
	want10 := "com.atlassian.greenhopper.service.sprint.Sprint@1a2b[activatedDate=2026-10-06T09:00:00.000Z," +
		"autoStartStop=false,completeDate=<null>,endDate=2026-10-20T00:00:00.000Z,goal=ship it,name=Fake\nline 2," +
		"id=7,incompleteIssuesDestinationId=<null>,name=Team | Sprint 3, part 2,rapidViewId=<null>,sequence=7," +
		"startDate=2026-10-06T09:00:00.000Z,state=CLOSED,synced=false]"
	if got8 != want8 {
		t.Errorf("Sprint8x = %q\nwant %q", got8, want8)
	}
	if got10 != want10 {
		t.Errorf("Sprint10x = %q\nwant %q", got10, want10)
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
