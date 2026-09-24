// Demo of the hedged fetcher: a slow primary, a fast hedge, a cache hit,
// and an aggregated both-sources-failed error.
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"hedged"
)

func since(start time.Time) string { return time.Since(start).Round(time.Millisecond).String() }

func main() {
	start := time.Now()

	primary := func(ctx context.Context, key string) (hedged.Result, error) {
		fmt.Printf("[%s] primary: start %q\n", since(start), key)
		select {
		case <-time.After(400 * time.Millisecond):
			fmt.Printf("[%s] primary: done\n", since(start))
			return hedged.NewResult([]byte("primary-v1"), 1), nil
		case <-ctx.Done():
			fmt.Printf("[%s] primary: canceled (loser)\n", since(start))
			return hedged.Result{}, ctx.Err()
		}
	}
	hedge := func(ctx context.Context, key string) (hedged.Result, error) {
		fmt.Printf("[%s] hedge:   start %q (speculative)\n", since(start), key)
		select {
		case <-time.After(80 * time.Millisecond):
			fmt.Printf("[%s] hedge:   done\n", since(start))
			return hedged.NewResult([]byte("hedge-v2"), 2), nil
		case <-ctx.Done():
			return hedged.Result{}, ctx.Err()
		}
	}

	f := hedged.NewFetcher(hedged.Config{
		Primary: primary, Hedge: hedge, Clock: hedged.RealClock{},
		HedgeDelay: 120 * time.Millisecond, MinVersion: 1, SpeculativeQuota: 4,
	})
	defer f.Close()

	fmt.Println("== 1. cache miss: primary is slow, hedge wins ==")
	t0 := time.Now()
	res, err := f.Get(context.Background(), "greeting")
	fmt.Printf("winner: %q v%d in %s (err=%v)\n", res.Value, res.Version, time.Since(t0).Round(time.Millisecond), err)
	time.Sleep(50 * time.Millisecond) // let the canceled loser log
	fmt.Println()

	fmt.Println("== 2. cache hit: no source calls ==")
	t0 = time.Now()
	res, err = f.Get(context.Background(), "greeting")
	fmt.Printf("cached: %q v%d in %s (err=%v)\n\n", res.Value, res.Version, time.Since(t0).Round(time.Millisecond), err)

	fmt.Println("== 3. both sources fail: aggregated error ==")
	errA := errors.New("primary: connection refused")
	errB := errors.New("hedge: 503 unavailable")
	broken := hedged.NewFetcher(hedged.Config{
		Primary: func(context.Context, string) (hedged.Result, error) { return hedged.Result{}, errA },
		Hedge:   func(context.Context, string) (hedged.Result, error) { return hedged.Result{}, errB },
		Clock:   hedged.RealClock{}, HedgeDelay: 50 * time.Millisecond, MinVersion: 1,
	})
	defer broken.Close()
	_, err = broken.Get(context.Background(), "missing")
	fmt.Printf("error: %v\n", err)
}
