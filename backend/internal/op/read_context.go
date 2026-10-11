package op

import (
	"context"
	"errors"

	"github.com/OpenListTeam/OpenList/v4/pkg/singleflight"
)

// Each caller may stop waiting independently. If the owner of a shared read
// cancels, a still-live waiter retries with its own context instead of inheriting
// another caller's cancellation. Never retry the caller's own failed operation.
func doReadWithContext[T any](ctx context.Context, group *singleflight.Group[T], key string, fn func() (T, error)) (T, error) {
	var zero T
	for {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		owned := false
		result := group.DoChan(key, func() (T, error) {
			owned = true
			return fn()
		})
		select {
		case <-ctx.Done():
			return zero, ctx.Err()
		case r := <-result:
			if err := ctx.Err(); err != nil {
				return zero, err
			}
			// Receiving the result synchronizes the read of owned with the worker.
			if !owned && (errors.Is(r.Err, context.Canceled) || errors.Is(r.Err, context.DeadlineExceeded)) {
				continue
			}
			return r.Val, r.Err
		}
	}
}
