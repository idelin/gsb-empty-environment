// Demo of the reactive Suspense resource: a search-as-you-type scenario
// where the user edits the query faster than the network responds.
package main

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"suspense"
)

// fakeSearch simulates a flaky network call with random latency.
// The query "alp" is deliberately very slow so it loses the race.
func fakeSearch(ctx context.Context, query string) (string, error) {
	latency := time.Duration(50+rand.Intn(100)) * time.Millisecond
	if query == "alp" {
		latency = 600 * time.Millisecond // stale, slow request
	}
	select {
	case <-time.After(latency):
		return fmt.Sprintf("hits for %q (latency %v)", query, latency), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func main() {
	rand.Seed(time.Now().UnixNano())
	start := time.Now()
	ts := func() string { return time.Since(start).Round(time.Millisecond).String() }

	query := suspense.NewSignal("a")
	res := suspense.NewResource(context.Background(), query, fakeSearch)
	defer res.Close()

	// Downstream UI: renders every state transition.
	var mu sync.Mutex
	res.Subscribe(func(s suspense.Snapshot[string]) {
		mu.Lock()
		defer mu.Unlock()
		switch s.State {
		case suspense.Pending:
			fmt.Printf("[%s] ⏳ Pending  (epoch %d) — showing spinner\n", ts(), s.Epoch)
		case suspense.Success:
			fmt.Printf("[%s] ✅ Success  (epoch %d) — render: %s\n", ts(), s.Epoch, s.Value)
		case suspense.Error:
			fmt.Printf("[%s] ❌ Error    (epoch %d) — %v\n", ts(), s.Epoch, s.Err)
		}
	})

	// User types quickly: "a" -> "al" -> "alp" -> "alpha".
	// The slow "alp" request is superseded before it resolves.
	for _, q := range []string{"al", "alp", "alpha"} {
		time.Sleep(80 * time.Millisecond)
		fmt.Printf("[%s] ✏️  user types %q\n", ts(), q)
		query.Set(q)
	}

	res.Wait()
	final := res.Snapshot()
	fmt.Printf("\nfinal state: %v — %s\n", final.State, final.Value)
	if final.State != suspense.Success {
		panic("expected success")
	}
}
