// Command threelayer demonstrates a shared attempt budget across three
// nested retry layers whose requests always fail. Without a shared
// budget, three layers retrying independently would multiply the number
// of requests; here the total is capped by the budget.
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	retrybudget "retrybudget"
)

var errDownstream = errors.New("downstream unavailable")

func main() {
	budget := retrybudget.New(5, retrybudget.WithTimeout(time.Minute))
	ctx := retrybudget.ContextWithBudget(context.Background(), budget)

	attempt := 0
	// Leaf: the only place a real request is issued (and charged).
	leaf := func(ctx context.Context) error {
		return retrybudget.Issue(ctx, func(ctx context.Context) error {
			attempt++
			fmt.Printf("request #%d issued -> %v\n", attempt, errDownstream)
			return errDownstream
		})
	}
	// Three retry layers, each unaware of the others' retry counts.
	layer3 := func(ctx context.Context) error { return retrybudget.Do(ctx, leaf) }
	layer2 := func(ctx context.Context) error { return retrybudget.Do(ctx, layer3) }
	layer1 := func(ctx context.Context) error { return retrybudget.Do(ctx, layer2) }

	err := retrybudget.Do(ctx, layer1)
	fmt.Printf("stopped: %v\n", err)
	fmt.Printf("total requests issued: %d (budget cap: %d, remaining: %d)\n",
		attempt, budget.Max(), budget.Remaining())
}
