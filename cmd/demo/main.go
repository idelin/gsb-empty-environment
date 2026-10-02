// Command demo simulates a page whose components subscribe to a global
// event stream, get destroyed, and are automatically deregistered by GC.
// It then shows the leak detector flagging a component that was never
// released.
package main

import (
	"log"
	"os"
	"runtime"
	"time"

	"weakbus/eventbus"
)

// component is a UI component living on a page.
type component struct {
	name string
}

// mountComponent creates a component, subscribes it to the global event
// stream, and drops every strong reference to it (the component is
// "destroyed").
//
//go:noinline
func mountComponent(bus *eventbus.Bus, name string) {
	c := &component{name: name}
	eventbus.Subscribe(bus, "global", c, func(ev eventbus.Event) {
		log.Printf("component %s received event: %v", name, ev.Payload)
	})
}

func main() {
	logger := log.New(os.Stderr, "[eventbus] ", 0)
	bus := eventbus.New()
	detector := eventbus.NewLeakDetector(bus, logger, 100*time.Millisecond)

	log.Println("--- phase 1: components subscribe, then are destroyed ---")
	mountComponent(bus, "Header")
	mountComponent(bus, "Sidebar")
	mountComponent(bus, "Footer")
	log.Printf("active subscribers after mount: %d", bus.TotalSubscribers())

	log.Println("forcing GC; waiting for automatic deregistration...")
	for i := 0; ; i++ {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
		n := bus.SubscriberCount("global")
		log.Printf("active subscribers: %d", n)
		if n == 0 {
			break
		}
		if i > 100 {
			log.Fatal("subscribers were not cleaned up")
		}
	}
	log.Printf("delivered to %d handlers after teardown (want 0)",
		bus.Publish("global", "page-refresh"))

	log.Println("--- phase 2: leak detection ---")
	leaked := &component{name: "LeakyWidget"}
	eventbus.Subscribe(bus, "global", leaked, func(ev eventbus.Event) {})
	time.Sleep(150 * time.Millisecond) // outlives its expected lifetime
	report := detector.Scan()
	runtime.KeepAlive(leaked)
	log.Printf("scan report: active=%d zombies=%d", report.Total(), len(report.Zombies))
}
