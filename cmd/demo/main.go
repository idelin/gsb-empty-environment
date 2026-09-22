// Command demo shows ordered.Map running tasks concurrently with random
// delays while results are delivered strictly in input order.
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
	in := make(chan int)
	go func() {
		defer close(in)
		for i := 0; i < 12; i++ {
			in <- i
		}
	}()

	fn := func(ctx context.Context, v int) (int, error) {
		d := time.Duration(rand.Intn(200)) * time.Millisecond
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return 0, ctx.Err()
		}
		fmt.Printf("  task %2d finished after %4v (completion order is random)\n", v, d)
		if v == 7 {
			return 0, errors.New("task 7 failed")
		}
		return v * v, nil
	}

	fmt.Println("completions (concurrent, unordered):")
	out := ordered.Map(context.Background(), in, 4, ordered.OrderedFail, fn)
	fmt.Println("deliveries (strictly ordered, stops at first error):")
	for r := range out {
		if r.Err != nil {
			fmt.Printf(">> index %2d: ERROR %v\n", r.Index, r.Err)
			continue
		}
		fmt.Printf(">> index %2d: value %d\n", r.Index, r.Value)
	}
}
