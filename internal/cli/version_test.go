package cli

import "testing"

func TestToolVersion(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ ldflags, build, want string }{
		{"dev", "v0.1.0", "v0.1.0"},
		{"v0.2.0", "v0.1.0", "v0.2.0"},
		{"v0.2.0", "(devel)", "v0.2.0"},
		{"dev", "(devel)", "dev"},
	} {
		if got := toolVersion(tc.ldflags, tc.build); got != tc.want {
			t.Errorf("toolVersion(%q, %q) = %q, want %q", tc.ldflags, tc.build, got, tc.want)
		}
	}
}
