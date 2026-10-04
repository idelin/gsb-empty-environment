// Command threelayer simulates a three-layer call stack (gateway ->
// service -> transport) where every layer retries independently and
// every request fails. Without a shared budget the layers would issue
// up to 4*4*4 = 64 requests; with the shared budget the total number
// of requests actually issued must equal the budget, no more.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"retrybudget/budget"
)

const maxAttempts = 5

var errUpstream = errors.New("upstream unavailable")

func main() {
	b := budget.New(maxAttempts).WithDeadline(time.Now().Add(10 * time.Second))
	ctx, stop := budget.NewContext(context.Background(), b)
	defer stop()

	var requests int64
	policy := budget.Policy{
		MaxRetries: 3, // each layer would try up to 4 times on its own
		Backoff:    func(int) time.Duration { return 5 * time.Millisecond },
	}

	// Transport layer: the only place a request is truly issued, so it
	// is the only place that spends a token.
	transport := func(ctx context.Context) error {
		return budget.Do(ctx, policy, func(_ context.Context, tok *budget.Token) error {
			if !tok.Spend() {
				return errors.New("token not spendable")
			}
			n := atomic.AddInt64(&requests, 1)
			fmt.Printf("  transport: issuing request #%d\n", n)
			return errUpstream
		})
	}
	// Service layer: retries the transport, never issues requests
	// itself, so its reservations are refunded after each attempt.
	service := func(ctx context.Context) error {
		return budget.Do(ctx, policy, func(ctx context.Context, _ *budget.Token) error {
			return transport(ctx)
		})
	}
	// Gateway layer: retries the service.
	gateway := func(ctx context.Context) error {
		return budget.Do(ctx, policy, func(ctx context.Context, _ *budget.Token) error {
			return service(ctx)
		})
	}

	err := gateway(ctx)
	fmt.Printf("gateway returned: %v\n", err)
	fmt.Printf("requests issued: %d, budget remaining: %d\n", requests, b.Remaining())

	if got := atomic.LoadInt64(&requests); got != maxAttempts {
		fmt.Printf("FAIL: issued %d requests, budget allows %d\n", got, maxAttempts)
		os.Exit(1)
	}
	if err == nil || !budget.IsBudgetError(err) {
		fmt.Printf("FAIL: expected a budget exhaustion error, got %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("OK: shared budget capped three retry layers at %d requests (naive maximum: 64)\n", maxAttempts)
}
