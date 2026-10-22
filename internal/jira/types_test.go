package jira_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/jira"
)

func TestParseTimeKeepsInstantAndMilliseconds(t *testing.T) {
	want := time.Date(2026, 10, 20, 9, 15, 40, 123e6, time.UTC)
	for _, s := range []string{
		"2026-10-20T09:15:40.123+0000",
		"2026-10-20T11:15:40.123+0200",
		"2026-10-20T04:15:40.123-0500",
	} {
		got, err := jira.ParseTime(s)
		if err != nil {
			t.Fatalf("ParseTime(%q): %v", s, err)
		}
		if !got.Equal(want) {
			t.Errorf("ParseTime(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestParseTimeIgnoresLocalZone(t *testing.T) {
	local := time.Local
	time.Local = time.FixedZone("CEST", 2*3600)
	t.Cleanup(func() { time.Local = local })

	got, err := jira.ParseTime("2026-10-20T11:15:40.123+0200")
	if err != nil {
		t.Fatal(err)
	}
	if name, offset := got.Zone(); name != "" || offset != 2*3600 {
		t.Errorf("Zone = %q %d, want \"\" 7200", name, offset)
	}
	got, err = jira.ParseTime("2026-10-20T09:15:40.123+0000")
	if err != nil {
		t.Fatal(err)
	}
	if got.Location() != time.UTC {
		t.Errorf("Location = %v, want UTC", got.Location())
	}
}

func TestParseTimeRejectsOtherFormats(t *testing.T) {
	for _, s := range []string{
		"",
		"2026-10-20T09:15:40.123+00:00",
		"2026-10-20T09:15:40.123Z",
		"2026-10-20T09:15:40+0000",
	} {
		_, err := jira.ParseTime(s)
		var parseErr *time.ParseError
		if !errors.As(err, &parseErr) {
			t.Errorf("ParseTime(%q) error = %v, want *time.ParseError", s, err)
		}
	}
}

func TestTimeLayoutFormatsJiraWireTime(t *testing.T) {
	ts := time.Date(2026, 10, 20, 9, 15, 40, 123e6, time.FixedZone("", -5*3600))
	if got, want := ts.Format(jira.TimeLayout), "2026-10-20T09:15:40.123-0500"; got != want {
		t.Errorf("Format = %q, want %q", got, want)
	}
}

func decode(t *testing.T, s string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(s), v); err != nil {
		t.Fatal(err)
	}
}

func encode(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUserJSON(t *testing.T) {
	var u jira.User
	decode(t, `{"self":"x","name":"jdoe","key":"JIRAUSER10100","emailAddress":"jdoe@example.com",
		"displayName":"Jane Doe","active":true,"timeZone":"Etc/UTC"}`, &u)
	want := jira.User{Name: "jdoe", Key: "JIRAUSER10100", DisplayName: "Jane Doe", EmailAddress: "jdoe@example.com", Active: true}
	if u != want {
		t.Errorf("User = %+v, want %+v", u, want)
	}
	if got, want := encode(t, jira.User{Name: "n"}), `{"name":"n","key":"","displayName":"","active":false}`; got != want {
		t.Errorf("Marshal = %s, want %s", got, want)
	}
}

func TestServerInfoJSON(t *testing.T) {
	var info jira.ServerInfo
	decode(t, `{"baseUrl":"https://jira.example.com","version":"10.3.4","versionNumbers":[10,3,4],
		"deploymentType":"Server","buildNumber":10030004,"serverTitle":"Jira"}`, &info)
	want := jira.ServerInfo{BaseURL: "https://jira.example.com", Version: "10.3.4", VersionNumbers: []int{10, 3, 4},
		DeploymentType: "Server", ServerTitle: "Jira"}
	if !reflect.DeepEqual(info, want) {
		t.Errorf("ServerInfo = %+v, want %+v", info, want)
	}
}

func TestJSONKeys(t *testing.T) {
	for _, tc := range []struct {
		v    any
		want string
	}{
		{jira.ServerInfo{}, `{"baseUrl":"","version":"","versionNumbers":null,"deploymentType":"","serverTitle":""}`},
		{jira.Field{}, `{"id":"","name":"","custom":false,"schema":{"type":""}}`},
		{jira.SearchPage{Issues: []jira.Issue{{}}},
			`{"startAt":0,"maxResults":0,"total":0,"issues":[{"id":"","key":"","fields":null}]}`},
		{jira.Comment{}, `{"id":"","author":null,"body":"","created":"","updated":""}`},
	} {
		if got := encode(t, tc.v); got != tc.want {
			t.Errorf("Marshal(%T) = %s, want %s", tc.v, got, tc.want)
		}
	}
}

func TestFieldJSON(t *testing.T) {
	var fields []jira.Field
	decode(t, `[
		{"id":"customfield_10100","name":"Sprint","custom":true,"orderable":true,"navigable":true,"searchable":true,
		 "clauseNames":["cf[10100]","Sprint"],
		 "schema":{"type":"array","items":"string","custom":"com.pyxis.greenhopper.jira:gh-sprint","customId":10100}},
		{"id":"summary","name":"Summary","custom":false,"schema":{"type":"string","system":"summary"}}
	]`, &fields)
	want := []jira.Field{
		{ID: "customfield_10100", Name: "Sprint", Custom: true, Schema: jira.FieldSchema{
			Type: "array", Items: "string", Custom: "com.pyxis.greenhopper.jira:gh-sprint", CustomID: 10100}},
		{ID: "summary", Name: "Summary", Schema: jira.FieldSchema{Type: "string", System: "summary"}},
	}
	if len(fields) != len(want) || fields[0] != want[0] || fields[1] != want[1] {
		t.Errorf("Fields = %+v, want %+v", fields, want)
	}
	if got, want := encode(t, jira.FieldSchema{Type: "string"}), `{"type":"string"}`; got != want {
		t.Errorf("Marshal = %s, want %s", got, want)
	}
}

func TestSearchPageJSON(t *testing.T) {
	var page jira.SearchPage
	decode(t, `{"expand":"schema,names","startAt":1000,"maxResults":1000,"total":1204,"issues":[
		{"expand":"operations","id":"10230","self":"x","key":"PLAT-1",
		 "fields":{"updated":"2026-10-20T09:15:40.123+0000","resolution":null}}]}`, &page)
	if page.StartAt != 1000 || page.MaxResults != 1000 || page.Total != 1204 || len(page.Issues) != 1 {
		t.Fatalf("SearchPage = %+v", page)
	}
	issue := page.Issues[0]
	if issue.ID != "10230" || issue.Key != "PLAT-1" {
		t.Errorf("Issue = %+v", issue)
	}
	if got := string(issue.Fields["updated"]); got != `"2026-10-20T09:15:40.123+0000"` {
		t.Errorf("updated = %s", got)
	}
	if got := string(issue.Fields["resolution"]); got != "null" {
		t.Errorf("resolution = %s", got)
	}
}

func TestCommentJSON(t *testing.T) {
	var comments []jira.Comment
	decode(t, `[
		{"self":"x","id":"30001","author":{"name":"jdoe","displayName":"Jane Doe","active":true},
		 "body":"line1\r\nline2","created":"2026-10-19T17:30:02.000+0000","updated":"2026-10-20T09:00:00.000+0200"},
		{"id":"30002","body":"anon","created":"c","updated":"u"}
	]`, &comments)
	if len(comments) != 2 {
		t.Fatalf("len(comments) = %d, want 2", len(comments))
	}
	c := comments[0]
	if c.ID != "30001" || c.Body != "line1\r\nline2" ||
		c.Created != "2026-10-19T17:30:02.000+0000" || c.Updated != "2026-10-20T09:00:00.000+0200" {
		t.Errorf("Comment = %+v", c)
	}
	if c.Author == nil || *c.Author != (jira.User{Name: "jdoe", DisplayName: "Jane Doe", Active: true}) {
		t.Errorf("Author = %+v", c.Author)
	}
	if comments[1].Author != nil {
		t.Errorf("anonymous Author = %+v, want nil", comments[1].Author)
	}
}
