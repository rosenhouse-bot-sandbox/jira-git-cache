package version_test

import (
	"testing"

	"github.com/rosenhouse-bot-sandbox/jira-git-cache/internal/version"
)

func TestIsRelease(t *testing.T) {
	for s, want := range map[string]bool{
		"v0.1.0":          true,
		"v10.20.300":      true,
		"0.1.0":           false,
		"v0.1":            false,
		"v0.1.0.1":        false,
		"v0.1.0-rc.1":     false,
		"v0.1.0\n":        false,
		"xv0.1.0":         false,
		"v0.1.a":          false,
		"dev":             false,
		"":                false,
		"(devel)":         false,
		"v1.2.3+dirty":    false,
		"v1.2.3-rc1+meta": false,
	} {
		if got := version.IsRelease(s); got != want {
			t.Errorf("IsRelease(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestPinnable(t *testing.T) {
	for s, want := range map[string]bool{
		"v0.1.0":                               true,
		"v0.1.0-rc.1":                          true,
		"v0.0.0-20261022070000-abcdef123456":   true,
		"v0.1.1-0.20261022070000-abcdef123456": true,
		"v0.1.0-":                              false,
		"v0.1.0-rc_1":                          false,
		"v0.1.0+incompatible":                  false,
		"v0.1.0+dirty":                         false,
		"v0.0.0-20261022070000-abcdef123456+dirty": false,
		"v0.1.0-rc.1\n": false,
		"0.1.0-rc.1":    false,
		"(devel)":       false,
		"dev":           false,
		"":              false,
	} {
		if got := version.Pinnable(s); got != want {
			t.Errorf("Pinnable(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestNewer(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"v0.2.0", "v0.1.0", true},
		{"v0.1.0", "v0.2.0", false},
		{"v0.1.0", "v0.1.0", false},
		{"v1.0.0", "v0.9.9", true},
		{"v0.10.0", "v0.9.0", true},
		{"v0.9.0", "v0.10.0", false},
		{"v0.1.10", "v0.1.9", true},
		{"v0.1.2", "v0.1.10", false},
		{"v2.0.0", "v10.0.0", false},
		{"v0.1.02", "v0.1.2", false},
		{"v0.1.3", "v0.1.02", true},
		{"v99999999999999999999.0.0", "v99999999999999999998.0.0", true},
		{"v2.0.0", "v1.0.0-rc1", false},
		{"v2.0.0-rc1", "v1.0.0", false},
		{"v1.0.0", "dev", false},
		{"dev", "v1.0.0", false},
		{"", "", false},
	} {
		if got := version.Newer(tc.a, tc.b); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
