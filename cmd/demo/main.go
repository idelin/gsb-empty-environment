// Command demo shows weighted fair sharing across two continuously saturated
// tenants with weights 1:2. A single global slot makes each completion a fresh
// weighted choice, so while both queues are saturated the service counts track
// 1:2. The global concurrency limit with multiple slots is demonstrated in the
// test suite (TestGlobalConcurrencyLimit).
package main

import (
	"context"
	"fmt"

	"scheduler"
)

func main() {
	s, err := scheduler.New(1, map[string]int{"a": 1, "b": 2})
	if err != nil {
		panic(err)
	}

	const perTenant = 1000

	// Each task rendezvous with the pacer: it signals that it started, then
	// waits for the pacer to release it. Nothing completes until both queues
	// are fully populated, so nearly every choice is between two saturated
	// queues.
	start := make(chan string, 1)
	done := make(chan struct{})
	work := func(id string) scheduler.TaskFunc {
		return func(context.Context) error {
			start <- id
			<-done
			return nil
		}
	}
	for range perTenant {
		if _, err := s.Submit("a", work("a")); err != nil {
			panic(err)
		}
		if _, err := s.Submit("b", work("b")); err != nil {
			panic(err)
		}
	}

	go func() {
		var a, b int
		for i := range 2 * perTenant {
			if <-start == "a" {
				a++
			} else {
				b++
			}
			if i == 299 {
				fmt.Printf("while saturated (first 300 starts): a=%d b=%d ratio b/a=%.2f\n", a, b, float64(b)/float64(a))
			}
			done <- struct{}{}
		}
	}()

	if err := s.Shutdown(context.Background()); err != nil {
		panic(err)
	}

	stats, _ := s.Stats()
	var sa, sb int64
	for _, st := range stats {
		if st.ID == "a" {
			sa = st.Served
		}
		if st.ID == "b" {
			sb = st.Served
		}
	}
	fmt.Printf("after drain (queues eventually empty): a=%d b=%d\n", sa, sb)
	fmt.Println("the 1:2 ratio is visible while both tenants remain saturated")

	if _, err := s.Submit("a", work("a")); err == scheduler.ErrClosed {
		fmt.Println("submission after shutdown rejected: ErrClosed")
	}
}
