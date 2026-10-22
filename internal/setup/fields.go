package setup

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

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
