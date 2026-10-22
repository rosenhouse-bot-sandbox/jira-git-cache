// Package snapshot holds the normalized data on the cache branch.
package snapshot

import (
	"cmp"
	"slices"
	"strings"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/jira"
)

const issueTimeLayout = "2006-01-02T15:04:05.000Z"

// Field is a custom field's id and Jira name.
type Field struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Path returns the issue file's path on the branch, such as "PLAT/PLAT-123.json".
func Path(key string) string { return Project(key) + "/" + key + ".json" }

// Project returns the project part of an issue key.
func Project(key string) string {
	project, _, _ := strings.Cut(key, "-")
	return project
}

// LessKey orders issue keys by project, then by number.
func LessKey(a, b string) bool {
	pa, na, _ := strings.Cut(a, "-")
	pb, nb, _ := strings.Cut(b, "-")
	return cmp.Or(strings.Compare(pa, pb), compareNumbers(na, nb)) < 0
}

// compareNumbers compares decimal strings without leading zeros.
func compareNumbers(a, b string) int {
	return cmp.Or(cmp.Compare(len(a), len(b)), strings.Compare(a, b))
}

// NormalizeTime converts a Jira time to UTC with milliseconds, such as "2026-10-20T09:15:40.123Z".
func NormalizeTime(jiraTime string) (string, error) {
	t, err := jira.ParseTime(jiraTime)
	if err != nil {
		return "", err
	}
	return t.UTC().Format(issueTimeLayout), nil
}

// FieldsFromJira returns the custom fields, sorted by the number in their id.
func FieldsFromJira(fs []jira.Field) []Field {
	out := []Field{}
	for _, f := range fs {
		if f.Custom {
			out = append(out, Field{ID: f.ID, Name: f.Name})
		}
	}
	slices.SortFunc(out, func(a, b Field) int {
		return compareNumbers(a.ID, b.ID)
	})
	return out
}
