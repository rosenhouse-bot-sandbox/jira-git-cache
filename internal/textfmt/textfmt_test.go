package textfmt_test

import (
	"testing"
	"time"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/textfmt"
)

func TestIssues(t *testing.T) {
	for n, want := range map[int]string{0: "0 issues", 1: "1 issue", 2: "2 issues", 1204: "1,204 issues"} {
		if got := textfmt.Issues(n); got != want {
			t.Errorf("Issues(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestCount(t *testing.T) {
	for n, want := range map[int]string{
		0:       "0",
		7:       "7",
		999:     "999",
		1000:    "1,000",
		1204:    "1,204",
		999999:  "999,999",
		1234567: "1,234,567",
	} {
		if got := textfmt.Count(n); got != want {
			t.Errorf("Count(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0:                                   "0s",
		400 * time.Millisecond:              "0s",
		500 * time.Millisecond:              "1s",
		30 * time.Second:                    "30s",
		161 * time.Second:                   "2m41s",
		10 * time.Minute:                    "10m",
		90 * time.Minute:                    "1h30m",
		time.Hour + 30*time.Second:          "1h0m30s",
		2 * time.Hour:                       "2h",
		24 * time.Hour:                      "24h",
		24*time.Hour + 499*time.Millisecond: "24h",
	} {
		if got := textfmt.Duration(d); got != want {
			t.Errorf("Duration(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestAge(t *testing.T) {
	for d, want := range map[time.Duration]string{
		-5 * time.Minute:                "0m",
		0:                               "0m",
		59 * time.Second:                "0m",
		time.Minute:                     "1m",
		12*time.Minute + 59*time.Second: "12m",
		59 * time.Minute:                "59m",
		time.Hour:                       "1h0m",
		3 * time.Hour:                   "3h0m",
		3*time.Hour + 11*time.Minute:    "3h11m",
		50 * time.Hour:                  "50h0m",
	} {
		if got := textfmt.Age(d); got != want {
			t.Errorf("Age(%v) = %q, want %q", d, got, want)
		}
	}
}
