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
	number     = regexp.MustCompile(`^\d+$`)
)

// IsRelease reports whether s matches ^v\d+\.\d+\.\d+$.
func IsRelease(s string) bool {
	return release.MatchString(s)
}

// Pinnable reports whether `go run pkg@s` can use s: a release, a pre-release or a pseudo-version.
func Pinnable(s string) bool {
	return IsRelease(s) || prerelease.MatchString(s)
}

// Newer reports whether a > b in semver order. It is false unless both are Pinnable.
func Newer(a, b string) bool {
	if !Pinnable(a) || !Pinnable(b) {
		return false
	}
	coreA, preA, _ := strings.Cut(a[1:], "-")
	coreB, preB, _ := strings.Cut(b[1:], "-")
	if c := slices.CompareFunc(strings.Split(coreA, "."), strings.Split(coreB, "."), compareNumbers); c != 0 {
		return c > 0
	}
	if preA == "" || preB == "" {
		return preA == "" && preB != ""
	}
	return slices.CompareFunc(strings.Split(preA, "."), strings.Split(preB, "."), compareIdentifiers) > 0
}

func compareNumbers(x, y string) int {
	x, y = strings.TrimLeft(x, "0"), strings.TrimLeft(y, "0")
	return cmp.Or(cmp.Compare(len(x), len(y)), strings.Compare(x, y))
}

// compareIdentifiers orders pre-release identifiers: numbers by value, and
// below any word.
func compareIdentifiers(x, y string) int {
	xNum, yNum := number.MatchString(x), number.MatchString(y)
	switch {
	case xNum && yNum:
		return compareNumbers(x, y)
	case xNum:
		return -1
	case yNum:
		return 1
	}
	return strings.Compare(x, y)
}
