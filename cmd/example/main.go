// Command example demonstrates scopex: one subtask fails, its siblings are
// canceled, cleanup completes before Run returns, and the first failure is
// preserved.
package main

import (
	"context"
	"fmt"
	"time"

	"scopex"
)

func worker(name string, failAfter time.Duration) func(context.Context) error {
	return func(ctx context.Context) error {
		timer := time.NewTimer(failAfter)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			fmt.Printf("%s: canceled, cleaning up...\n", name)
			time.Sleep(100 * time.Millisecond) // simulate cleanup work
			fmt.Printf("%s: cleanup done\n", name)
			return nil
		case <-timer.C:
			fmt.Printf("%s: failed!\n", name)
			return fmt.Errorf("%s: %w", name, errBoom)
		}
	}
}

var errBoom = fmt.Errorf("boom")

func main() {
	start := time.Now()
	err := scopex.Run(context.Background(), func(s *scopex.Scope) error {
		s.Go(worker("fetch", 200*time.Millisecond)) // this one fails
		s.Go(worker("parse", 5*time.Second))        // canceled sibling
		s.Scope(func(child *scopex.Scope) error {   // nested scope
			child.Go(worker("store", 5*time.Second))
			return nil
		})
		return nil
	})
	fmt.Printf("Run returned after %s, err=%v\n", time.Since(start).Round(time.Millisecond), err)
}
