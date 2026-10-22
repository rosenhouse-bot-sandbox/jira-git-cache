package snapshot

import (
	"cmp"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/jira"
)

// SystemFields are the Jira fields FromJira reads, besides custom fields.
var SystemFields = []string{"summary", "status", "resolution", "issuetype", "priority", "assignee",
	"reporter", "labels", "parent", "issuelinks", "created", "updated", "description"}

// User is a Jira user.
type User struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

// Link is an issue link, seen from the issue that holds it.
type Link struct {
	Relation string `json:"relation"` // such as "blocks" or "is blocked by"
	Key      string `json:"key"`
}

// Comment is an issue comment. Body is raw wiki markup.
type Comment struct {
	ID      string `json:"id"`
	Author  *User  `json:"author"`
	Created string `json:"created"`
	Updated string `json:"updated"`
	Body    string `json:"body"`
}

// Issue is one normalized issue. Times are UTC with milliseconds.
type Issue struct {
	ID          string         `json:"id"`
	Key         string         `json:"key"`
	Summary     string         `json:"summary"`
	Status      string         `json:"status"`
	Resolution  *string        `json:"resolution"`
	Type        string         `json:"type"`
	Priority    *string        `json:"priority"`
	Assignee    *User          `json:"assignee"`
	Reporter    *User          `json:"reporter"`
	Labels      []string       `json:"labels"`
	Parent      *string        `json:"parent"`
	Links       []Link         `json:"links"`
	Created     string         `json:"created"`
	Updated     string         `json:"updated"`
	Description *string        `json:"description"`
	Custom      map[string]any `json:"custom"`   // by configured name; null when unset
	Comments    []Comment      `json:"comments"` // sorted by created, then numeric id
}

type named struct {
	Name *string `json:"name"`
}

type issueRef struct {
	Key string `json:"key"`
}

type wireFields struct {
	Summary    *string    `json:"summary"`
	Status     *named     `json:"status"`
	Resolution *named     `json:"resolution"`
	IssueType  *named     `json:"issuetype"`
	Priority   *named     `json:"priority"`
	Assignee   *jira.User `json:"assignee"`
	Reporter   *jira.User `json:"reporter"`
	Labels     []string   `json:"labels"`
	Parent     *issueRef  `json:"parent"`
	IssueLinks []struct {
		Type struct {
			Inward  string `json:"inward"`
			Outward string `json:"outward"`
		} `json:"type"`
		InwardIssue  *issueRef `json:"inwardIssue"`
		OutwardIssue *issueRef `json:"outwardIssue"`
	} `json:"issuelinks"`
	Created     *string `json:"created"`
	Updated     *string `json:"updated"`
	Description *string `json:"description"`
}

// FromJira normalizes an issue and its comments. customNames maps custom field ids to configured names.
func FromJira(iss jira.Issue, comments []jira.Comment, customNames map[string]string) (Issue, error) {
	system := map[string]json.RawMessage{}
	for _, id := range SystemFields {
		system[id] = iss.Fields[id]
	}
	raw, err := json.Marshal(system)
	if err != nil {
		return Issue{}, fmt.Errorf("%s: %w", iss.Key, err)
	}
	var w wireFields
	if err := json.Unmarshal(raw, &w); err != nil {
		return Issue{}, fmt.Errorf("%s: %w", iss.Key, err)
	}
	status, typ := nameOf(w.Status), nameOf(w.IssueType)
	for _, r := range []struct {
		field string
		value *string
	}{{"summary", w.Summary}, {"status.name", status}, {"issuetype.name", typ}, {"created", w.Created}, {"updated", w.Updated}} {
		if r.value == nil {
			return Issue{}, fmt.Errorf("%s: missing %s", iss.Key, r.field)
		}
	}
	created, err := NormalizeTime(*w.Created)
	if err != nil {
		return Issue{}, fmt.Errorf("%s: created: %w", iss.Key, err)
	}
	updated, err := NormalizeTime(*w.Updated)
	if err != nil {
		return Issue{}, fmt.Errorf("%s: updated: %w", iss.Key, err)
	}
	out := Issue{
		ID:          iss.ID,
		Key:         iss.Key,
		Summary:     *w.Summary,
		Status:      *status,
		Resolution:  nameOf(w.Resolution),
		Type:        *typ,
		Priority:    nameOf(w.Priority),
		Assignee:    userOf(w.Assignee),
		Reporter:    userOf(w.Reporter),
		Labels:      append([]string{}, w.Labels...),
		Links:       []Link{},
		Created:     created,
		Updated:     updated,
		Description: w.Description,
		Custom:      map[string]any{},
	}
	if w.Parent != nil {
		out.Parent = &w.Parent.Key
	}
	for _, l := range w.IssueLinks {
		if l.OutwardIssue != nil {
			out.Links = append(out.Links, Link{Relation: l.Type.Outward, Key: l.OutwardIssue.Key})
		} else if l.InwardIssue != nil {
			out.Links = append(out.Links, Link{Relation: l.Type.Inward, Key: l.InwardIssue.Key})
		}
	}
	for id, name := range customNames {
		var v any
		if raw, ok := iss.Fields[id]; ok {
			if err := json.Unmarshal(raw, &v); err != nil {
				return Issue{}, fmt.Errorf("%s: %s: %w", iss.Key, id, err)
			}
		}
		out.Custom[name] = normalize(v)
	}
	if out.Comments, err = commentsFromJira(comments); err != nil {
		return Issue{}, fmt.Errorf("%s: %w", iss.Key, err)
	}
	return out, nil
}

func nameOf(n *named) *string {
	if n == nil {
		return nil
	}
	return n.Name
}

func userOf(u *jira.User) *User {
	if u == nil {
		return nil
	}
	return &User{Name: u.Name, DisplayName: u.DisplayName}
}

func commentsFromJira(comments []jira.Comment) ([]Comment, error) {
	out := []Comment{}
	seen := map[string]bool{}
	for _, c := range comments {
		if seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		created, err := NormalizeTime(c.Created)
		if err != nil {
			return nil, fmt.Errorf("comment %s: created: %w", c.ID, err)
		}
		updated, err := NormalizeTime(c.Updated)
		if err != nil {
			return nil, fmt.Errorf("comment %s: updated: %w", c.ID, err)
		}
		out = append(out, Comment{ID: c.ID, Author: userOf(c.Author), Created: created, Updated: updated, Body: c.Body})
	}
	slices.SortFunc(out, func(a, b Comment) int {
		return cmp.Or(strings.Compare(a.Created, b.Created), compareNumbers(a.ID, b.ID))
	})
	return out, nil
}

var valueKeys = []string{"value", "displayName", "name", "key"}

func normalize(v any) any {
	switch v := v.(type) {
	case string:
		return sprintName(v)
	case []any:
		out := []any{}
		for _, e := range v {
			if n := normalize(e); n != nil {
				out = append(out, n)
			}
		}
		return out
	case map[string]any:
		return normalizeObject(v)
	default:
		return v
	}
}

func normalizeObject(o map[string]any) any {
	hasValueKey := false
	for _, k := range valueKeys {
		v, ok := o[k]
		hasValueKey = hasValueKey || ok
		s, ok := v.(string)
		if !ok {
			continue
		}
		if child, _ := o["child"].(map[string]any); k == "value" && child != nil {
			if cv, ok := child["value"].(string); ok {
				return s + " > " + cv
			}
		}
		return s
	}
	if !hasValueKey && o["showField"] == false {
		return nil
	}
	return o
}

var (
	sprintDump = regexp.MustCompile(`(?s)^com\.atlassian\.greenhopper\.service\.sprint\.Sprint@[0-9a-f]+\[(.*)\]$`)
	sprintKey  = regexp.MustCompile(`,(?:activatedDate|autoStartStop|completeDate|endDate|goal|id|incompleteIssuesDestinationId|name|rapidViewId|sequence|startDate|state|synced)=`)
)

// sprintName returns the name in a sprint's toString dump, or s itself.
func sprintName(s string) string {
	m := sprintDump.FindStringSubmatch(s)
	if m == nil {
		return s
	}
	for _, seg := range sprintSegments(m[1]) {
		if k, v, _ := strings.Cut(seg, "="); k == "name" {
			return v
		}
	}
	return s
}

// sprintSegments splits a dump body into "key=value" segments.
// A repeated key right after goal is goal's free text, so it is merged back into goal.
func sprintSegments(body string) []string {
	var parts []string
	start := 0
	for _, loc := range sprintKey.FindAllStringIndex(body, -1) {
		parts = append(parts, body[start:loc[0]])
		start = loc[0] + 1
	}
	parts = append(parts, body[start:])

	count := map[string]int{}
	for _, p := range parts {
		count[segmentKey(p)]++
	}
	var out []string
	inGoal := false
	for _, p := range parts {
		k := segmentKey(p)
		if inGoal && count[k] > 1 {
			out[len(out)-1] += "," + p
			continue
		}
		out = append(out, p)
		inGoal = k == "goal"
	}
	return out
}

func segmentKey(seg string) string {
	k, _, _ := strings.Cut(seg, "=")
	return k
}
