package setup_test

import (
	"slices"
	"testing"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/setup"
)

func TestSnakeName(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"Story Points":   "story_points",
		"Legacy Ticket#": "legacy_ticket",
		"  Epic  Link ":  "epic_link",
		"a-b_c":          "a_b_c",
		"Café au lait":   "caf_au_lait",
		"2nd Reviewer":   "2nd_reviewer",
		"###":            "",
		"`a{z/0:9@Z[":    "a_z_0_9_z",
	} {
		if got := setup.SnakeName(in); got != want {
			t.Errorf("SnakeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseSelection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		n    int
		want []int
	}{
		{"1-4", 5, []int{1, 2, 3, 4}},
		{"1,3", 5, []int{1, 3}},
		{" 2 , 4-5 ", 5, []int{2, 4, 5}},
		{"3,1,3,2-3", 5, []int{1, 2, 3}},
		{"3-3", 5, []int{3}},
		{"1", 1, []int{1}},
		{"none", 0, []int{}},
		{" None ", 3, []int{}},
	} {
		got, err := setup.ParseSelection(tc.in, tc.n)
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("ParseSelection(%q, %d) = %v, %v; want %v", tc.in, tc.n, got, err, tc.want)
		}
	}
}

func TestParseSelectionRejects(t *testing.T) {
	t.Parallel()
	for _, in := range []string{"", "0", "6", "1-6", "0-2", "4-3", "a", "1-", "-1", "1,,2", "1-2-3"} {
		if got, err := setup.ParseSelection(in, 5); err == nil {
			t.Errorf("ParseSelection(%q, 5) = %v, want error", in, got)
		}
	}
	_, err := setup.ParseSelection("6", 5)
	if want := `invalid selection "6": use numbers 1-5, ranges like 1-3, commas, or none`; err == nil || err.Error() != want {
		t.Errorf("got %v, want %s", err, want)
	}
}
