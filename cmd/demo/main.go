// Demo: retry a flaky job along an explicit backoff sequence with the real
// clock, then cancel a second job during its backoff wait.
package main

import (
	"context"
	"fmt"
	"time"

	"retrysched"
)

func main() {
	sched := retrysched.NewScheduler(retrysched.RealClock{},
		[]time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond})

	// Job 1: fails twice, succeeds on attempt 3.
	start := time.Now()
	h1 := sched.Submit("flaky-job", func(_ context.Context, attempt int) error {
		fmt.Printf("[%6.0fms] flaky-job attempt %d\n", float64(time.Since(start).Milliseconds()), attempt)
		if attempt < 3 {
			return fmt.Errorf("transient error %d", attempt)
		}
		return nil
	})
	r1 := h1.Wait()
	fmt.Printf("flaky-job done: err=%v attempts=%d history=", r1.Err, len(r1.Attempts))
	for _, a := range r1.Attempts {
		fmt.Printf("[#%d err=%v] ", a.Attempt, a.Err)
	}
	fmt.Println()

	// Job 2: always fails; canceled during the 400ms backoff without waiting.
	h2 := sched.Submit("doomed-job", func(_ context.Context, attempt int) error {
		fmt.Printf("[%6.0fms] doomed-job attempt %d\n", float64(time.Since(start).Milliseconds()), attempt)
		return fmt.Errorf("still broken")
	})
	time.Sleep(150 * time.Millisecond) // attempt 1 failed, backoff timer pending
	cancelStart := time.Now()
	h2.Cancel()
	r2 := h2.Wait()
	fmt.Printf("doomed-job done: err=%v attempts=%d cancel-returned-in=%v\n",
		r2.Err, len(r2.Attempts), time.Since(cancelStart).Round(time.Millisecond))

	// Job 3: exhausts the backoff sequence; result carries last error+history.
	h3 := sched.Submit("hopeless-job", func(_ context.Context, attempt int) error {
		return fmt.Errorf("failure #%d", attempt)
	})
	r3 := h3.Wait()
	fmt.Printf("hopeless-job done: err=%q attempts=%d\n", r3.Err, len(r3.Attempts))
}
