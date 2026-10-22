package snapshot_test

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/jira"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/jiramock"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/snapshot"
)

func exampleSnapshot(t *testing.T) snapshot.Snapshot {
	t.Helper()
	iss, comments := fromMock(t, jiramock.ExampleIssue())
	example, err := snapshot.FromJira(iss, comments, map[string]string{
		"customfield_10100": "sprint", "customfield_10002": "story_points", "customfield_10700": "parent_link",
	})
	if err != nil {
		t.Fatal(err)
	}
	minimal, err := snapshot.FromJira(fromMockIssue(t, jiramock.NewIssue("INFRA-77", 30077, updated)), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	fetched := time.Date(2026, 10, 22, 7, 0, 41, 0, time.FixedZone("", 2*3600))
	full := time.Date(2026, 10, 21, 23, 0, 0, 0, time.UTC)
	return snapshot.Snapshot{
		Meta: snapshot.Meta{
			FetchedAt:  &fetched,
			LastFullAt: &full,
			JiraURL:    "https://jira.example.com",
			Projects: map[string]snapshot.ProjectMeta{
				"PLAT":  {JQL: `project = "PLAT" AND updated >= "-30d"`, Count: 1},
				"INFRA": {JQL: `project = "INFRA" AND updated >= "-30d"`, Count: 1},
			},
			SchemaVersion: 1,
			ToolVersion:   "v0.1.0",
			FieldsHash:    "sha256:abc",
		},
		Fields: []snapshot.Field{{ID: "customfield_10002", Name: "Story Points"}},
		Issues: map[string]snapshot.Issue{"PLAT-123": example, "INFRA-77": minimal},
	}
}

func fromMockIssue(t *testing.T, m jiramock.Issue) jira.Issue {
	t.Helper()
	iss, _ := fromMock(t, m)
	return iss
}

func TestEncodeWritesMetaFieldsAndIssues(t *testing.T) {
	files := exampleSnapshot(t).Encode()

	wantMeta := `{
  "fetched_at": "2026-10-22T05:00:41Z",
  "last_full_at": "2026-10-21T23:00:00Z",
  "jira_url": "https://jira.example.com",
  "projects": {
    "INFRA": {
      "jql": "project = \"INFRA\" AND updated >= \"-30d\"",
      "count": 1
    },
    "PLAT": {
      "jql": "project = \"PLAT\" AND updated >= \"-30d\"",
      "count": 1
    }
  },
  "schema_version": 1,
  "tool_version": "v0.1.0",
  "fields_hash": "sha256:abc"
}
`
	if got := string(files["_meta/snapshot.json"]); got != wantMeta {
		t.Errorf("snapshot.json:\n%s\nwant:\n%s", got, wantMeta)
	}
	wantFields := `[
  {
    "id": "customfield_10002",
    "name": "Story Points"
  }
]
`
	if got := string(files["_meta/fields.json"]); got != wantFields {
		t.Errorf("fields.json:\n%s\nwant:\n%s", got, wantFields)
	}
	var paths []string
	for p := range files {
		paths = append(paths, p)
	}
	if want := []string{"INFRA/INFRA-77.json", "PLAT/PLAT-123.json", "_meta/fields.json", "_meta/snapshot.json"}; !sameSet(paths, want) {
		t.Errorf("paths = %v, want %v", paths, want)
	}
}

func sameSet(a, b []string) bool {
	set := map[string]int{}
	for _, s := range a {
		set[s]++
	}
	for _, s := range b {
		set[s]--
	}
	for _, n := range set {
		if n != 0 {
			return false
		}
	}
	return true
}

func TestEncodeTruncatesMetaTimesToUTCSeconds(t *testing.T) {
	fetched := time.Date(2026, 10, 22, 7, 0, 41, 999e6, time.FixedZone("", 2*3600))
	files := snapshot.Snapshot{Meta: snapshot.Meta{FetchedAt: &fetched, SchemaVersion: 1}}.Encode()

	if got := string(files["_meta/snapshot.json"]); !strings.Contains(got, `"fetched_at": "2026-10-22T05:00:41Z",`) {
		t.Errorf("snapshot.json:\n%s", got)
	}
}

func TestEncodeEmptySnapshot(t *testing.T) {
	files := snapshot.Snapshot{Meta: snapshot.Meta{JiraURL: "https://jira.example.com", SchemaVersion: 1}}.Encode()

	want := map[string][]byte{"_meta/snapshot.json": []byte(`{
  "fetched_at": null,
  "last_full_at": null,
  "jira_url": "https://jira.example.com",
  "projects": {},
  "schema_version": 1,
  "tool_version": "",
  "fields_hash": ""
}
`)}
	if !reflect.DeepEqual(files, want) {
		t.Errorf("got %q, want %q", files, want)
	}
}

func TestEncodeWritesEmptyFieldsList(t *testing.T) {
	files := snapshot.Snapshot{Fields: []snapshot.Field{}}.Encode()

	if got := string(files["_meta/fields.json"]); got != "[]\n" {
		t.Errorf("fields.json = %q", got)
	}
}

func TestEncodeIssue(t *testing.T) {
	got := string(snapshot.EncodeIssue(snapshot.Issue{
		ID:          "1",
		Key:         "PLAT-1",
		Summary:     "a <b> & c",
		Description: ptr("x\r\ny"),
		Custom:      map[string]any{"z": 1.5, "a": []any{"<x>"}},
	}))

	want := `{
  "id": "1",
  "key": "PLAT-1",
  "summary": "a <b> & c",
  "status": "",
  "resolution": null,
  "type": "",
  "priority": null,
  "assignee": null,
  "reporter": null,
  "labels": [],
  "parent": null,
  "links": [],
  "created": "",
  "updated": "",
  "description": "x\r\ny",
  "custom": {
    "a": [
      "<x>"
    ],
    "z": 1.5
  },
  "comments": []
}
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestEncodeIssueWritesEmptyCustom(t *testing.T) {
	if got := string(snapshot.EncodeIssue(snapshot.Issue{})); !strings.Contains(got, `"custom": {},`) {
		t.Errorf("got:\n%s", got)
	}
}

func TestEncodeIssuePanicsOnUnencodableValue(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("want panic")
		}
	}()
	snapshot.EncodeIssue(snapshot.Issue{Custom: map[string]any{"x": math.NaN()}})
}

func TestDecodeRoundTrips(t *testing.T) {
	s := exampleSnapshot(t)
	files := s.Encode()

	got, err := snapshot.Decode(files)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Encode(), files) {
		t.Error("re-encoding changed bytes")
	}
	if !got.Meta.FetchedAt.Equal(*s.Meta.FetchedAt) || got.Meta.FetchedAt.Location() != time.UTC {
		t.Errorf("FetchedAt = %v", got.Meta.FetchedAt)
	}
	got.Meta.FetchedAt, s.Meta.FetchedAt = nil, nil
	if !reflect.DeepEqual(got, s) {
		t.Errorf("got  %+v\nwant %+v", got, s)
	}
}

func TestDecodeEmptySnapshot(t *testing.T) {
	got, err := snapshot.Decode(map[string][]byte{"_meta/snapshot.json": []byte(`{"fetched_at":null,"projects":null,"schema_version":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	want := snapshot.Snapshot{
		Meta:   snapshot.Meta{Projects: map[string]snapshot.ProjectMeta{}, SchemaVersion: 1},
		Issues: map[string]snapshot.Issue{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestDecodeRequiresSnapshotJSON(t *testing.T) {
	_, err := snapshot.Decode(map[string][]byte{"PLAT/PLAT-1.json": []byte("{}")})
	if err == nil || err.Error() != "not a jira-git-cache snapshot" {
		t.Errorf("err = %v", err)
	}
}

func TestDecodeRefusesNewerSchema(t *testing.T) {
	_, err := snapshot.Decode(map[string][]byte{"_meta/snapshot.json": []byte(`{"schema_version":2,"projects":[]}`)})

	var newer *snapshot.NewerSchemaError
	if !errors.As(err, &newer) || *newer != (snapshot.NewerSchemaError{Got: 2, Supported: 1}) {
		t.Fatalf("err = %v", err)
	}
	if want := "snapshot schema_version 2 is newer than this jira-git-cache supports (1)"; err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}

func TestDecodeRejectsBadMeta(t *testing.T) {
	for _, meta := range []string{
		`{`,
		`{}`,
		`{"schema_version":0}`,
		`{"schema_version":"1"}`,
		`{"schema_version":1,"projects":[]}`,
		`{"schema_version":1,"fetched_at":"2026-10-22 05:00:41"}`,
		`{"schema_version":1,"last_full_at":"2026-10-22T05:00:41+02:00"}`,
	} {
		_, err := snapshot.Decode(map[string][]byte{"_meta/snapshot.json": []byte(meta)})
		var newer *snapshot.NewerSchemaError
		if err == nil || errors.As(err, &newer) {
			t.Errorf("%s: err = %v", meta, err)
		}
	}
}

func TestDecodeRejectsBadFieldsJSON(t *testing.T) {
	files := exampleSnapshot(t).Encode()
	files["_meta/fields.json"] = []byte(`{}`)

	if _, err := snapshot.Decode(files); err == nil || !strings.Contains(err.Error(), "_meta/fields.json") {
		t.Errorf("err = %v", err)
	}
}

func TestDecodeRejectsMisplacedOrInvalidIssues(t *testing.T) {
	plat1 := exampleSnapshot(t).Encode()["PLAT/PLAT-123.json"]
	for path, body := range map[string][]byte{
		"PLAT/PLAT-12.json":   plat1,
		"INFRA/PLAT-123.json": plat1,
		"PLAT/PLAT-1.json":    []byte(`{"key":`),
	} {
		files := exampleSnapshot(t).Encode()
		files[path] = body

		if _, err := snapshot.Decode(files); err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("%s: err = %v", path, err)
		}
	}
}

func TestDecodeIgnoresOtherPaths(t *testing.T) {
	files := snapshot.Snapshot{Meta: snapshot.Meta{SchemaVersion: 1}}.Encode()
	for _, p := range []string{
		"README.md", "PLAT/notes.json", "plat/PLAT-1.json", "PLAT/PLAT-01.json", "PLAT/PLAT-0.json",
		"PLAT/sub/PLAT-1.json", "PLAT/PLAT-1.json.bak", "x/PLAT/PLAT-1.json", "PLAT/PLAT-1xjson", "1PLAT/1PLAT-1.json",
	} {
		files[p] = []byte("garbage")
	}

	got, err := snapshot.Decode(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Issues) != 0 {
		t.Errorf("Issues = %v", got.Issues)
	}
}
