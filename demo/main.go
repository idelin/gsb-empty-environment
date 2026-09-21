// Demo: a flaky task retried with an explicit backoff sequence.
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"tasksched"
)

func main() {
	backoff := []time.Duration{200 * time.Millisecond, 500 * time.Millisecond, time.Second}
	calls := 0
	task := func(ctx context.Context, attempt int) error {
		calls++
		fmt.Printf("[%s] task=%q attempt=%d start\n",
			time.Now().Format("15:04:05.000"), "daily-report", attempt)
		if attempt < 3 {
			return errors.New("downstream unavailable")
		}
		return nil
	}

	out, err := tasksched.Run(context.Background(), tasksched.RealClock{},
		"daily-report", 5, backoff, task)

	fmt.Println("--- result ---")
	fmt.Printf("err=%v totalCalls=%d attempts=%d\n", err, calls, len(out.Attempts))
	for _, a := range out.Attempts {
		fmt.Printf("  attempt %d at %s err=%v\n",
			a.Number, a.StartedAt.Format("15:04:05.000"), a.Err)
	}
}
