package snapshot_test

import (
	"reflect"
	"testing"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/jira"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/snapshot"
)

func TestPathAndProject(t *testing.T) {
	if got := snapshot.Path("PLAT-123"); got != "PLAT/PLAT-123.json" {
		t.Errorf("Path = %q", got)
	}
	if got := snapshot.Project("MY_PROJ2-7"); got != "MY_PROJ2" {
		t.Errorf("Project = %q", got)
	}
}

func TestLessKey(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"PLAT-2", "PLAT-10", true},
		{"PLAT-10", "PLAT-2", false},
		{"PLAT-9", "PLAT-10", true},
		{"PLAT-12", "PLAT-21", true},
		{"PLAT-21", "PLAT-12", false},
		{"PLAT-5", "PLAT-5", false},
		{"INFRA-900", "PLAT-1", true},
		{"PLAT-1", "INFRA-900", false},
		{"A-2", "AB-1", true},
	} {
		if got := snapshot.LessKey(tc.a, tc.b); got != tc.want {
			t.Errorf("LessKey(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestNormalizeTimeConvertsToUTCMilliseconds(t *testing.T) {
	for in, want := range map[string]string{
		"2026-10-20T11:15:40.123+0200": "2026-10-20T09:15:40.123Z",
		"2026-10-20T04:15:40.000-0500": "2026-10-20T09:15:40.000Z",
		"2026-10-20T09:15:40.007+0000": "2026-10-20T09:15:40.007Z",
	} {
		got, err := snapshot.NormalizeTime(in)
		if err != nil {
			t.Fatalf("NormalizeTime(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("NormalizeTime(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeTimeRejectsOtherFormats(t *testing.T) {
	if _, err := snapshot.NormalizeTime("2026-10-20T09:15:40Z"); err == nil {
		t.Error("want error")
	}
}

func TestFieldsFromJiraKeepsCustomFieldsInNumericOrder(t *testing.T) {
	got := snapshot.FieldsFromJira([]jira.Field{
		{ID: "customfield_10100", Name: "Sprint", Custom: true},
		{ID: "summary", Name: "Summary"},
		{ID: "customfield_10002", Name: "Story Points", Custom: true},
		{ID: "customfield_9999", Name: "Old", Custom: true},
	})
	want := []snapshot.Field{
		{ID: "customfield_9999", Name: "Old"},
		{ID: "customfield_10002", Name: "Story Points"},
		{ID: "customfield_10100", Name: "Sprint"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestFieldsFromJiraIsNeverNil(t *testing.T) {
	if got := snapshot.FieldsFromJira([]jira.Field{{ID: "summary"}}); got == nil || len(got) != 0 {
		t.Errorf("got %#v, want empty non-nil", got)
	}
}
