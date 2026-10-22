package syncer

import (
	"context"
	"sync"
)

// forEach runs fn on items with at most limit calls in flight.
// The first error cancels the ctx of the other calls and is returned once they end.
func forEach[T any](ctx context.Context, limit int, items []T, fn func(context.Context, T) error) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	next := make(chan T)
	var wg sync.WaitGroup
	for range limit {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for item := range next {
				if err := fn(ctx, item); err != nil {
					cancel(err)
				}
			}
		}()
	}
feed:
	for _, item := range items {
		select {
		case next <- item:
		case <-ctx.Done():
			break feed
		}
	}
	close(next)
	wg.Wait()
	return context.Cause(ctx) //nolint:wrapcheck // fn's own error, or the parent ctx's
}
