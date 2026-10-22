package syncer

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
)

func TestForEachRunsEveryItemWithinLimit(t *testing.T) {
	var mu sync.Mutex
	var seen []int
	inFlight, peak := 0, 0
	release := make(chan struct{})

	err := forEach(t.Context(), 3, []int{1, 2, 3, 4, 5, 6, 7}, func(_ context.Context, n int) error {
		mu.Lock()
		seen = append(seen, n)
		inFlight++
		peak = max(peak, inFlight)
		if inFlight == 3 && len(seen) == 3 {
			close(release)
		}
		mu.Unlock()
		<-release
		mu.Lock()
		inFlight--
		mu.Unlock()
		return nil
	})

	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(seen)
	if !slices.Equal(seen, []int{1, 2, 3, 4, 5, 6, 7}) {
		t.Errorf("seen = %v", seen)
	}
	if peak != 3 {
		t.Errorf("peak in flight = %d, want 3", peak)
	}
}

func TestForEachCancelsOthersAndReturnsFirstError(t *testing.T) {
	boom := errors.New("boom")

	err := forEach(t.Context(), 2, []int{1, 2, 3}, func(ctx context.Context, n int) error {
		if n == 1 {
			<-ctx.Done()
			return ctx.Err()
		}
		return boom
	})

	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want boom", err)
	}
}

func TestForEachStopsCallingAfterFirstError(t *testing.T) {
	calls := 0

	err := forEach(t.Context(), 1, make([]int, 100), func(context.Context, int) error {
		calls++
		return errors.New("boom")
	})

	if err == nil || calls != 1 {
		t.Errorf("err = %v after %d calls, want an error after 1", err, calls)
	}
}

func TestForEachReturnsParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	calls := 0

	err := forEach(ctx, 1, []int{1}, func(context.Context, int) error {
		calls++
		return nil
	})

	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Errorf("err = %v after %d calls, want context.Canceled after 0", err, calls)
	}
}
