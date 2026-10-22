// Package textfmt formats numbers and durations for people to read.
package textfmt

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Count formats n >= 0 with comma thousands separators: 1204 → "1,204".
func Count(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// Duration formats d rounded to the second, dropping zero trailing units:
// 24h → "24h", 2h → "2h", 90m → "1h30m", 161s → "2m41s", 0 → "0s".
func Duration(d time.Duration) string {
	s := d.Round(time.Second).String()
	if rest, ok := strings.CutSuffix(s, "m0s"); ok {
		s = rest + "m"
	}
	if rest, ok := strings.CutSuffix(s, "h0m"); ok {
		s = rest + "h"
	}
	return s
}

// Age formats d truncated to the minute: 12m → "12m", 3h11m → "3h11m", 3h → "3h0m", <1m → "0m".
func Age(d time.Duration) string {
	minutes := max(d, 0) / time.Minute
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dh%dm", minutes/60, minutes%60)
}
