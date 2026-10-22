package main

import (
	"testing"
	"time"
)

func TestHTTPClientTimesOut(t *testing.T) {
	if got := newHTTPClient().Timeout; got != time.Minute {
		t.Errorf("Timeout = %v, want 1m", got)
	}
}
