// Package version parses and compares jira-git-cache version strings.
package version

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
)

var (
	release    = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
	prerelease = regexp.MustCompile(`^v\d+\.\d+\.\d+-[0-9A-Za-z.-]+$`)
)

// IsRelease reports whether s matches ^v\d+\.\d+\.\d+$.
func IsRelease(s string) bool {
	return release.MatchString(s)
}

// Pinnable reports whether `go run pkg@s` can use s: a release, a pre-release or a pseudo-version.
func Pinnable(s string) bool {
	return IsRelease(s) || prerelease.MatchString(s)
}

// Newer reports whether a > b. It is false unless both are releases.
func Newer(a, b string) bool {
	if !IsRelease(a) || !IsRelease(b) {
		return false
	}
	return slices.CompareFunc(strings.Split(a[1:], "."), strings.Split(b[1:], "."), compareNumbers) > 0
}

func compareNumbers(x, y string) int {
	x, y = strings.TrimLeft(x, "0"), strings.TrimLeft(y, "0")
	return cmp.Or(cmp.Compare(len(x), len(y)), strings.Compare(x, y))
}
