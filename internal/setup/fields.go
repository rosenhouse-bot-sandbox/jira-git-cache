package setup

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/config"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/jira"
	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/snapshot"
)

const sampleSize = 100

// SnakeName turns a Jira field name into a config name: "Story Points" → "story_points".
// Runs of characters other than a-z and 0-9 become one underscore.
func SnakeName(jiraName string) string {
	var b strings.Builder
	gap := false
	for _, r := range strings.ToLower(jiraName) {
		if r < '0' || r > '9' && r < 'a' || r > 'z' {
			gap = true
			continue
		}
		if gap && b.Len() > 0 {
			b.WriteByte('_')
		}
		b.WriteRune(r)
		gap = false
	}
	return b.String()
}

// ParseSelection parses 1-based choices among n, such as "1-4", "1,3" or "none".
// The result is deduplicated and sorted.
func ParseSelection(s string, n int) ([]int, error) {
	if strings.EqualFold(strings.TrimSpace(s), "none") {
		return []int{}, nil
	}
	chosen := map[int]bool{}
	for _, part := range strings.Split(s, ",") {
		lo, hi, isRange := strings.Cut(part, "-")
		if !isRange {
			hi = lo
		}
		first, errLo := strconv.Atoi(strings.TrimSpace(lo))
		last, errHi := strconv.Atoi(strings.TrimSpace(hi))
		if errLo != nil || errHi != nil || first < 1 || first > last || last > n {
			return nil, fmt.Errorf("invalid selection %q: use numbers 1-%d, ranges like 1-3, commas, or none", strings.TrimSpace(part), n)
		}
		for i := first; i <= last; i++ {
			chosen[i] = true
		}
	}
	return slices.Sorted(maps.Keys(chosen)), nil
}

type candidate struct {
	field       jira.Field
	setOn       int
	preselected bool
}

func (s *session) chooseFields(ctx context.Context, client *jira.Client, projects []string) (config.Fields, error) {
	page, err := client.SearchPage(ctx, config.SampleJQL(projects), []string{"*all"}, 0, sampleSize)
	if err != nil {
		return nil, err
	}
	all, err := client.Fields(ctx)
	if err != nil {
		return nil, err
	}
	candidates, err := fieldsSet(page.Issues, all)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		s.printf("No custom fields set on %d recent issues.\n", len(page.Issues))
		return nil, nil
	}
	def := s.printFields(candidates, len(page.Issues))
	chosen, err := askValid(s, fmt.Sprintf("Include which fields? [%s]: ", def), def, "fields", s.Answers.Fields,
		func(v string) ([]int, error) { return ParseSelection(v, len(candidates)) })
	if err != nil {
		return nil, err
	}
	return configFields(candidates, chosen), nil
}

// fieldsSet returns the custom fields that hold a value on any of issues, most used first.
func fieldsSet(issues []jira.Issue, all []jira.Field) ([]candidate, error) {
	ids := map[string]string{}
	for _, iss := range issues {
		for id := range iss.Fields {
			if strings.HasPrefix(id, "customfield_") {
				ids[id] = id
			}
		}
	}
	setOn := map[string]int{}
	for _, iss := range issues {
		normalized, err := snapshot.FromJira(iss, nil, ids)
		if err != nil {
			return nil, err
		}
		for id, v := range normalized.Custom {
			if isSet(v) {
				setOn[id]++
			}
		}
	}
	byID := map[string]jira.Field{}
	for _, f := range all {
		byID[f.ID] = f
	}
	var candidates []candidate
	for id, n := range setOn {
		f := byID[id]
		f.ID = id
		candidates = append(candidates, candidate{field: f, setOn: n, preselected: preselected(f)})
	}
	slices.SortFunc(candidates, func(a, b candidate) int {
		return cmp.Or(cmp.Compare(b.setOn, a.setOn), strings.Compare(a.field.ID, b.field.ID))
	})
	return candidates, nil
}

func isSet(v any) bool {
	switch v := v.(type) {
	case nil:
		return false
	case string:
		return v != ""
	case []any:
		return len(v) > 0
	}
	return true
}

func preselected(f jira.Field) bool {
	switch f.Schema.Custom {
	case "com.pyxis.greenhopper.jira:gh-sprint", "com.pyxis.greenhopper.jira:gh-epic-link":
		return true
	}
	return strings.EqualFold(f.Name, "Story Points")
}

// printFields prints the candidates table and returns the default selection.
func (s *session) printFields(candidates []candidate, sampled int) string {
	idW, nameW := 0, len("Jira name")
	for _, c := range candidates {
		idW = max(idW, len(c.field.ID))
		nameW = max(nameW, utf8.RuneCountInString(c.field.Name))
	}
	s.printf("Custom fields set on %d recent issues:\n", sampled)
	s.printf("     %-*s  %-*s  set on\n", idW, "id", nameW, "Jira name")
	var def []string
	for i, c := range candidates {
		mark := ""
		if c.preselected {
			mark = "  *"
			def = append(def, strconv.Itoa(i+1))
		}
		s.printf("%3d  %-*s  %-*s %6d%s\n", i+1, idW, c.field.ID, nameW, c.field.Name, c.setOn, mark)
	}
	if len(def) == 0 {
		return "none"
	}
	return strings.Join(def, ",")
}

// configFields names the chosen candidates for config.yml.
func configFields(candidates []candidate, chosen []int) config.Fields {
	var fields config.Fields
	used := map[string]bool{}
	for _, i := range chosen {
		f := candidates[i-1].field
		digits := strings.TrimPrefix(f.ID, "customfield_")
		name := SnakeName(f.Name)
		if name == "" || name[0] < 'a' {
			name = "field_" + digits
		}
		if used[name] {
			name += "_" + digits
		}
		used[name] = true
		fields = append(fields, config.Field{ID: f.ID, Name: name})
	}
	return fields
}
