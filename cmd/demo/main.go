// Demo: component A subscribes to the global event stream, gets destroyed,
// and the bus's weak-reference machinery plus leak detector clean it up.
//
// Phase 1: a running watchdog reaps the finalized subscription automatically.
// Phase 2: with the watchdog stalled, the leak detector flags the lingering
// listener as a zombie and force-removes it.
package main

import (
	"log"
	"os"
	"runtime"
	"time"

	eventbus "weakbus"
)

func main() {
	logger := log.New(os.Stdout, "[eventbus] ", log.LstdFlags|log.Lmicroseconds)
	bus := eventbus.New(
		eventbus.WithZombieTimeout(100*time.Millisecond),
		eventbus.WithLogger(logger),
	)

	// ---- Phase 1: automatic cleanup via watchdog ----
	stop := bus.StartWatchdog(50 * time.Millisecond)

	// Component A subscribes to the global event stream.
	func() {
		sub := bus.Subscribe("global", func(payload interface{}) {
			log.Printf("component A received: %v", payload)
		})
		log.Printf("component A subscribed (topic=%s), active=%d", sub.Topic(), bus.ActiveCount())
		bus.Publish("global", "hello, A")
		// Component A is destroyed here: `sub` goes out of scope with no
		// other strong reference.
	}()

	log.Printf("component A destroyed, active=%d (awaiting GC)", bus.ActiveCount())
	for i := 0; i < 20 && bus.ActiveCount() > 0; i++ {
		runtime.GC()
		time.Sleep(60 * time.Millisecond)
	}
	stats := bus.Stats()
	log.Printf("after GC: active=%d pending=%d cleaned=%d", stats.Active, stats.Pending, stats.Cleaned)
	stop()

	// ---- Phase 2: zombie detection when sweeping stalls ----
	func() {
		sub := bus.Subscribe("global", func(interface{}) {})
		_ = sub // component B destroyed without unsubscribing
	}()
	for i := 0; i < 20 && bus.Stats().Pending == 0; i++ {
		runtime.GC()
		time.Sleep(30 * time.Millisecond)
	}
	time.Sleep(150 * time.Millisecond) // let the corpse age past the zombie timeout
	swept, zombies := bus.Sweep()
	log.Printf("manual sweep: swept=%d zombies=%d", swept, zombies)

	stats = bus.Stats()
	log.Printf("final: active=%d pending=%d cleaned=%d", stats.Active, stats.Pending, stats.Cleaned)
	if stats.Active != 0 {
		log.Print("LEAK: listeners still registered")
		os.Exit(1)
	}
	log.Print("OK: no leaked listeners — active subscriber count is zero")
}
