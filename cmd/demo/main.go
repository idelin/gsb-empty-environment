// Demo: three workers in one scope. Worker 2 fails after 200ms; the scope
// cancels the others, waits for their cleanup, and reports the aggregate.
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"scope"
)

func worker(name string, work, cleanup time.Duration, fail error) func(context.Context) error {
	return func(ctx context.Context) error {
		fmt.Printf("[%s] started\n", name)
		select {
		case <-time.After(work):
			if fail != nil {
				fmt.Printf("[%s] failed: %v\n", name, fail)
				return fail
			}
			fmt.Printf("[%s] finished normally\n", name)
			return nil
		case <-ctx.Done():
			fmt.Printf("[%s] cancelled (cause: %v), cleaning up %s...\n", name, context.Cause(ctx), cleanup)
			time.Sleep(cleanup) // cleanup must complete before Wait returns
			fmt.Printf("[%s] cleanup done\n", name)
			return ctx.Err()
		}
	}
}

func main() {
	start := time.Now()
	s := scope.New(context.Background())

	s.Go(worker("fetcher", 2*time.Second, 300*time.Millisecond, nil))
	s.Go(worker("parser", 200*time.Millisecond, 0, errors.New("invalid payload")))
	s.Go(worker("writer", 2*time.Second, 150*time.Millisecond, nil))

	err := s.Wait()
	fmt.Printf("scope returned after %s\n", time.Since(start).Round(time.Millisecond))
	fmt.Printf("aggregated error: %v\n", err)
}
