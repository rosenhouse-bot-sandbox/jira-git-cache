package setup

import (
	"fmt"
	"testing"
)

func TestParseBranch(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"dev/jira-cache", "a.b/c-d_e@f", "v1.2", "x-", "HEADS", "@x"} {
		if got, err := parseBranch(ok); got != ok || err != nil {
			t.Errorf("%q: %q, %v", ok, got, err)
		}
	}
	for _, bad := range []string{
		"-x", "HEAD", "@", ".x", "a/.x", "a.lock", "a.lock/b", "a..b", "a@{b", "a//b", "/a", "a/", "a.",
		"a b", "a\tb", "a\x7fb", "a~1", "a^", "a:b", "a?", "a*", "a[b", `a\b`,
	} {
		want := fmt.Sprintf("cache_branch: invalid branch name %q", bad)
		if _, err := parseBranch(bad); err == nil || err.Error() != want {
			t.Errorf("%q: %v", bad, err)
		}
	}
}
