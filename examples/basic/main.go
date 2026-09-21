// Command basic demonstrates the pubsub broker: independent bounded
// subscriber queues, block vs explicit-drop slow-consumer policies,
// concurrent unsubscribe, and close/drain semantics.
package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"pubsub"
)

func main() {
	b := pubsub.NewBroker()

	// Fast subscriber: a small bounded queue with the blocking policy.
	fast, err := b.Subscribe(pubsub.SubscriberConfig{Buffer: 16, Overflow: pubsub.OverflowBlock})
	must(err)
	go func() {
		// Consume the first burst eagerly (never slow) but stop at 10 so
		// the "final" message remains committed in the queue for draining.
		for i := 0; i < 10; i++ {
			<-fast.C()
		}
	}()

	// Slow subscriber: bounded queue that explicitly discards overflow and
	// reports each drop through OnDrop instead of losing it silently.
	var dropped int64
	slow, err := b.Subscribe(pubsub.SubscriberConfig{
		Buffer:   2,
		Overflow: pubsub.OverflowDropNewest,
		OnDrop:   func(any) { atomic.AddInt64(&dropped, 1) },
	})
	must(err)

	// An ephemeral subscriber that unsubscribes concurrently.
	ephemeral, err := b.Subscribe(pubsub.SubscriberConfig{Buffer: 8})
	must(err)
	go ephemeral.Unsubscribe() // safe even while publishes are in flight

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	for i := 0; i < 10; i++ {
		// A slow subscriber never blocks this publish or hurts "fast".
		if err := b.Publish(ctx, i); err != nil {
			panic(err)
		}
	}

	// Give the eager fast reader a moment, then inspect counters.
	time.Sleep(10 * time.Millisecond)
	st := b.Stats()
	fmt.Printf("published=%d delivered=%d dropped=%d\n", st.Published, st.Delivered, st.Dropped)

	// The slow queue keeps at most its capacity; the rest were explicit.
	kept := 0
loop:
	for {
		select {
		case <-slow.C():
			kept++
		default:
			break loop
		}
	}
	fmt.Printf("slow subscriber retained=%d, explicit drops=%d\n", kept, atomic.LoadInt64(&dropped))

	// Commit a final message, close, and drain it: shutdown preserves
	// messages already promised to subscribers.
	must(b.Publish(context.Background(), "final"))
	must(b.Close())
	<-fast.Done()
	committed := fast.Drain()
	fmt.Printf("after close, fast drained committed=%d (incl. final=%v)\n",
		len(committed), committed[len(committed)-1] == "final")

	// Idempotent shutdown/unsubscribe never panic.
	must(b.Close())
	slow.Unsubscribe()
	slow.Unsubscribe()
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
