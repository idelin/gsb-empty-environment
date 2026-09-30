package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"time"
)

func main() {
	var (
		mode        = flag.String("mode", "synth", "event source: synth or replay")
		fixturePath = flag.String("fixture", "fixtures/events.jsonl", "fixture file for replay/emit")
		emit        = flag.Bool("emit", false, "write synthesized events to the fixture file and exit")
		numEvents   = flag.Int("events", 300, "number of events to synthesize")
		numTenants  = flag.Int("tenants", 4, "number of tenants to synthesize")
		highRatio   = flag.Float64("high-ratio", 0.5, "fraction of high-priority events")
		seed        = flag.Int64("seed", 1, "synth random seed")
		capacity    = flag.Int("capacity", 32, "queue capacity")
		workers     = flag.Int("workers", 4, "worker count")
		highBurst   = flag.Int("high-burst", DefaultHighBurst, "max consecutive high-priority picks per tenant")
		policy      = flag.String("policy", "reject", "drop policy when full: reject or drop-oldest")
		shutdown    = flag.String("shutdown", "drain", "shutdown mode: drain or stop")
		rate        = flag.Int("rate", 0, "producer events per second (0 = unlimited burst)")
	)
	flag.Parse()

	var events []Event
	var err error
	switch *mode {
	case "synth":
		events = SynthEvents(*seed, *numEvents, *numTenants, *highRatio)
	case "replay":
		events, err = ReadFixture(*fixturePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "read fixture:", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "unknown -mode:", *mode)
		os.Exit(1)
	}

	if *emit {
		if err := WriteFixture(*fixturePath, events); err != nil {
			fmt.Fprintln(os.Stderr, "write fixture:", err)
			os.Exit(1)
		}
		fmt.Printf("wrote %d events to %s\n", len(events), *fixturePath)
		return
	}

	dropPolicy := PolicyRejectNew
	if *policy == "drop-oldest" {
		dropPolicy = PolicyDropOldest
	}
	shutdownMode := Drain
	if *shutdown == "stop" {
		shutdownMode = StopNow
	}

	q := NewQueue(*capacity, *highBurst, dropPolicy)
	handler := func(Event) {
		time.Sleep(2 * time.Millisecond) // simulate downstream processing cost
	}
	pool := NewPool(q, handler)
	pool.Start(*workers)

	var ticker *time.Ticker
	if *rate > 0 {
		ticker = time.NewTicker(time.Second / time.Duration(*rate))
		defer ticker.Stop()
	}
	for _, ev := range events {
		if ticker != nil {
			<-ticker.C
		}
		q.Enqueue(ev)
	}
	q.Close(shutdownMode)
	pool.Wait()

	snap := q.Snapshot()
	fmt.Printf("mode=%s events=%d capacity=%d workers=%d policy=%s shutdown=%s\n",
		*mode, len(events), *capacity, *workers, *policy, *shutdown)
	fmt.Printf("received=%d processed=%d dropped=%d\n", snap.Received, snap.Processed, snap.Dropped)
	reasons := make([]string, 0, len(snap.DropsByReason))
	for reason := range snap.DropsByReason {
		reasons = append(reasons, string(reason))
	}
	sort.Strings(reasons)
	for _, reason := range reasons {
		fmt.Printf("drops[%s]=%d\n", reason, snap.DropsByReason[DropReason(reason)])
	}
	tenants := make([]string, 0, len(snap.Tenants))
	for tenant := range snap.Tenants {
		tenants = append(tenants, tenant)
	}
	sort.Strings(tenants)
	for _, tenant := range tenants {
		ts := snap.Tenants[tenant]
		fmt.Printf("tenant %s: received=%d processed=%d dropped=%d\n",
			tenant, ts.Received, ts.Processed, ts.Dropped)
	}
	if snap.Conserved() {
		fmt.Println("conservation: OK (received == processed + dropped)")
	} else {
		fmt.Println("conservation: VIOLATED")
		os.Exit(1)
	}
}
