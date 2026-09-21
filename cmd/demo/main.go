// Runnable sample: two flat subtasks (one fails, one is cleaning up) and a
// nested child scope. Run with: go run ./cmd/demo
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	taskscope "scope-demo/scope"
)

var errBoom = errors.New("boom: injected failure")

func main() {
	fmt.Println("== flat scope: failure cancels sibling, Wait waits for cleanup ==")
	s := taskscope.New(context.Background())

	s.Go(func(ctx context.Context) error {
		time.Sleep(20 * time.Millisecond)
		fmt.Println("subtask A: failing")
		return errBoom
	})

	s.Go(func(ctx context.Context) error {
		<-ctx.Done()
		fmt.Println("subtask B: cancelled, starting cleanup ...")
		time.Sleep(150 * time.Millisecond) // injected cleanup duration
		fmt.Println("subtask B: cleanup done")
		return ctx.Err() // cancellation result, ignored by Wait
	})

	start := time.Now()
	if err := s.Wait(); err != nil {
		fmt.Printf("Wait returned after %v: %v\n\n", time.Since(start).Round(time.Millisecond), err)
	}

	fmt.Println("== nested scope: parent failure propagates to the child ==")
	root := taskscope.New(context.Background())
	root.Go(func(ctx context.Context) error {
		child := taskscope.New(ctx)
		child.Go(func(ctx context.Context) error {
			<-ctx.Done()
			fmt.Println("nested child: cancelled by parent, cleaning up ...")
			time.Sleep(80 * time.Millisecond)
			return ctx.Err()
		})
		return child.Wait()
	})
	root.Go(func(context.Context) error {
		time.Sleep(20 * time.Millisecond)
		return errors.New("parent: injected failure")
	})

	if err := root.Wait(); err != nil {
		fmt.Printf("root Wait: %v\n", err)
	}
	fmt.Println("\nrepeated explicit cancellation is safe:")
	c := taskscope.New(context.Background())
	c.Cancel()
	c.Cancel()
	fmt.Printf("Wait on cancelled empty scope: %v\n", c.Wait())
}
