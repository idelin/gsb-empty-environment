package scope_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"scope"
)

var errDiskFull = errors.New("disk full")

// One task fails, its sibling is cancelled and cleans up, and Wait
// returns the original cause once everything has finished.
func Example() {
	s := scope.New(context.Background())

	s.Go(func(ctx context.Context) error {
		<-ctx.Done()                 // cancelled by the sibling's failure
		time.Sleep(time.Millisecond) // simulate cleanup work
		fmt.Println("worker: cleaned up after", ctx.Err())
		return ctx.Err()
	})
	s.Go(func(ctx context.Context) error {
		return errDiskFull
	})

	err := s.Wait()
	fmt.Println("wait:", err)
	fmt.Println("cause is disk full:", errors.Is(err, errDiskFull))

	// Output:
	// worker: cleaned up after context canceled
	// wait: scope: disk full (and 1 more error(s))
	// cause is disk full: true
}
