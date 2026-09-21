// Command demo runs a narrated priority-inheritance scenario:
//
//  1. low (prio 1) holds "db"; high (prio 10) blocks on it -> low is boosted.
//  2. mid (prio 5) holds "cache" and blocks on "db"; high's priority
//     propagates transitively to mid.
//  3. high's wait is canceled -> both boosts collapse.
//  4. A wait-for cycle is attempted and reported as a deadlock.
package main

import (
	"context"
	"fmt"
	"time"

	"tasklock"
)

func waitFor(what string, cond func() bool) {
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			panic("timed out waiting for " + what)
		}
		time.Sleep(time.Millisecond)
	}
}

func main() {
	var s tasklock.Scheduler
	db := s.NewMutex("db")
	cache := s.NewMutex("cache")
	low := s.NewTask("low", 1)
	mid := s.NewTask("mid", 5)
	high := s.NewTask("high", 10)
	bg := context.Background()

	show := func(what string) {
		fmt.Printf("%-58s low=%d mid=%d high=%d\n", what,
			low.EffectivePriority(), mid.EffectivePriority(), high.EffectivePriority())
	}

	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}

	must(s.Lock(bg, low, db))
	must(s.Lock(bg, mid, cache))
	show("initial: low holds db, mid holds cache")

	// mid blocks on db (held by low).
	go func() {
		if err := s.Lock(bg, mid, db); err == nil {
			_ = s.Unlock(mid, db)
		}
	}()
	waitFor("mid to block on db", func() bool { return db.NumWaiters() == 1 })
	show("mid blocks on db (held by low): mid donates 5 to low")

	// high blocks on cache (held by mid): priority propagates mid -> low.
	ctx, cancel := context.WithCancel(bg)
	highDone := make(chan error, 1)
	go func() { highDone <- s.Lock(ctx, high, cache) }()
	waitFor("high to block on cache", func() bool { return cache.NumWaiters() == 1 })
	show("high blocks on cache (held by mid): 10 propagates to low")

	// Cancel high: every donated boost collapses.
	cancel()
	fmt.Println("cancel high's wait:", <-highDone)
	show("after high cancels: boosts revoked")

	// Attempt a cycle: low (holds db) waits on cache (held by mid, who
	// waits on db). The scheduler rejects it with a diagnosable cycle.
	err := s.Lock(bg, low, cache)
	fmt.Println("low tries to also lock cache:", err)
}
