// Command demo exercises the two-level cache with two cache nodes sharing
// one source, one L2 and one invalidation bus: strong vs. eventual reads,
// delayed invalidation notifications, duplicate/rollback rejection, L2
// failure degradation, and resource release.
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	cache "example.com/twocache"
)

func main() {
	ctx := context.Background()
	src := cache.NewMapSource()
	l2 := cache.NewL2()
	notifier := cache.NewNotifier()
	events := func(msg string) { fmt.Println("  [event]", msg) }

	nodeA := cache.New(cache.Options{Source: src, L2: l2, Notifier: notifier, OnEvent: events})
	nodeB := cache.New(cache.Options{Source: src, L2: l2, Notifier: notifier, OnEvent: events})

	fmt.Println("== 1. write on nodeA + strong read on nodeA ==")
	if _, err := nodeA.Write(ctx, "user:1", "alice"); err != nil {
		panic(err)
	}
	r, _ := nodeA.Get(ctx, "user:1", cache.Strong)
	fmt.Println("  strong:", r)

	fmt.Println("== 2. nodeB writes; notification delayed: eventual is declared-stale, strong waits ==")
	release := make(chan struct{})
	notifier.Delay = func() { <-release } // hold the invalidation in flight
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		_, _ = nodeB.Write(ctx, "user:1", "alice-v2") // publish blocks until release
	}()
	time.Sleep(50 * time.Millisecond) // write reached source+L2, notification still in flight
	r, _ = nodeA.Get(ctx, "user:1", cache.Eventual)
	fmt.Println("  nodeA eventual (notification in flight):", r)

	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	strongDone := make(chan cache.Result, 1)
	go func() {
		sr, err := nodeA.Get(waitCtx, "user:1", cache.Strong)
		if err != nil {
			fmt.Println("  nodeA strong error:", err)
		}
		strongDone <- sr
	}()
	time.Sleep(50 * time.Millisecond)
	fmt.Println("  ...nodeA strong read is waiting for the invalidation notification...")
	close(release) // notification finally delivered
	<-writeDone
	notifier.Delay = nil
	fmt.Println("  nodeA strong (after notification):", <-strongDone)
	cancel()
	waitForWatermark(nodeA, "user:1", 2)
	r, _ = nodeA.Get(ctx, "user:1", cache.Eventual)
	fmt.Println("  nodeA eventual (converged after invalidation):", r)

	fmt.Println("== 3. duplicate invalidation is idempotent, rollback rejected ==")
	wm := nodeA.Watermark("user:1")
	msg := cache.Message{Src: "manual", Key: "user:1", Version: wm, Seq: 999}
	_ = nodeA.ApplyInvalidation(msg)
	_ = nodeA.ApplyInvalidation(msg) // duplicate delivery
	err := nodeA.ApplyInvalidation(cache.Message{Src: "manual", Key: "user:1", Version: wm - 1, Seq: 1000})
	fmt.Println("  rollback apply error:", err)
	fmt.Println("  nodeA stats:", nodeA.Stats())

	fmt.Println("== 4. L2 failure: explicit degradation, never fake strong ==")
	l2.GetErr = func(string) error { return errors.New("L2 connection refused") }
	r, _ = nodeA.Get(ctx, "user:1", cache.Strong)
	fmt.Println("  strong, L2 down (source answers):", r)
	src.LoadErr = func(string) error { return errors.New("source timeout") }
	_, err = nodeA.Get(ctx, "user:1", cache.Strong)
	fmt.Println("  strong, L2+source down:", err)
	// Watermark moves past nodeA's L1 copy, so L1 is known-stale.
	_ = nodeA.ApplyInvalidation(cache.Message{Src: "manual", Key: "user:1", Version: 3, Seq: 1001})
	r, _ = nodeA.Get(ctx, "user:1", cache.Eventual)
	fmt.Println("  eventual, L2+source down (declared stale):", r)
	src.LoadErr = nil
	l2.GetErr = nil

	fmt.Println("== 5. resource release ==")
	fmt.Println("  subscribers before close:", notifier.SubscriberCount())
	_ = nodeA.Close()
	_ = nodeB.Close()
	fmt.Println("  subscribers after close: ", notifier.SubscriberCount())
	_, err = nodeA.Get(ctx, "user:1", cache.Eventual)
	fmt.Println("  get after close:", err)
}

func waitForWatermark(c *cache.Cache, key string, v uint64) {
	for c.Watermark(key) < v {
		time.Sleep(5 * time.Millisecond)
	}
}
