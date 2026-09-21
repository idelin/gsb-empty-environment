// Demo: concurrent execution with strictly ordered output.
// Item 0 is artificially slow; later items finish first but cannot overtake
// it, and the bounded buffer throttles the pipeline (backpressure).
package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"ordered"
)

func main() {
	ctx := context.Background()

	in := make(chan int)
	go func() {
		defer close(in)
		for i := 0; i < 10; i++ {
			in <- i
		}
	}()

	work := func(ctx context.Context, i int) (int, error) {
		if i == 0 {
			time.Sleep(500 * time.Millisecond) // slow head
		} else {
			time.Sleep(time.Duration(rand.Intn(50)) * time.Millisecond)
		}
		if i == 7 {
			return 0, errors.New("item 7 exploded")
		}
		return i * i, nil
	}

	fmt.Println("== FailInOrder: ordered results, then the in-order error ==")
	start := time.Now()
	for r := range ordered.Process(ctx, in, work, ordered.Options{
		Workers: 4, MaxBuffered: 2, Policy: ordered.FailInOrder,
	}) {
		if r.Err != nil {
			fmt.Printf("error: %v (stream terminates)\n", r.Err)
			break
		}
		fmt.Printf("value=%d (t=%v)\n", r.Value, time.Since(start).Round(time.Millisecond))
	}

	fmt.Println("== FailFast: first error aborts immediately ==")
	in2 := make(chan int)
	go func() {
		defer close(in2)
		for i := 0; i < 10; i++ {
			in2 <- i
		}
	}()
	start = time.Now()
	for r := range ordered.Process(ctx, in2, work, ordered.Options{
		Workers: 4, MaxBuffered: 2, Policy: ordered.FailFast,
	}) {
		if r.Err != nil {
			fmt.Printf("error: %v (t=%v, remaining work cancelled)\n", r.Err, time.Since(start).Round(time.Millisecond))
			break
		}
		fmt.Printf("value=%d (t=%v)\n", r.Value, time.Since(start).Round(time.Millisecond))
	}
}
