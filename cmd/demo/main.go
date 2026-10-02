// Command demo shows a persistent reactive store surviving a process
// restart. Run it twice:
//
//	go run ./cmd/demo   # phase 1: mutate state, write checkpoint.json
//	go run ./cmd/demo   # phase 2: fresh process, hydrate from checkpoint
package main

import (
	"fmt"
	"log"
	"os"

	"signalstore/reactive"
)

const checkpointPath = "checkpoint.json"

type app struct {
	store   *reactive.Store
	counter *reactive.Signal[int]
	user    *reactive.Signal[string]
	session *reactive.Signal[string] // transient, never persisted
	doubled *reactive.Computed[int]  // derived, recomputed after hydration
}

// buildApp wires the exact same state tree in every process run.
func buildApp() *app {
	store := reactive.NewStore()
	a := &app{
		store:   store,
		counter: reactive.NewSignal(store, "@persist counter", 0),
		user:    reactive.NewSignal(store, "@persist user", "anonymous"),
		session: reactive.NewSignal(store, "session", "<none>"),
	}
	a.doubled = reactive.NewComputed(store, "doubled", func() int {
		return a.counter.Get() * 2
	})
	reactive.NewEffect(func() {
		fmt.Printf("  [effect] counter=%d doubled=%d user=%q session=%q\n",
			a.counter.Get(), a.doubled.Get(), a.user.Get(), a.session.Get())
	})
	return a
}

func main() {
	log.SetFlags(0)
	if _, err := os.Stat(checkpointPath); err == nil {
		recoverFromCheckpoint()
		return
	}
	writeCheckpoint()
}

func writeCheckpoint() {
	fmt.Println("== phase 1: live process ==")
	a := buildApp()

	fmt.Println("-- mutating state --")
	a.counter.Set(21)
	a.user.Set("alice")
	a.session.Set("sess-7f3c (transient)")

	if err := a.store.SaveCheckpoint(checkpointPath); err != nil {
		log.Fatalf("save checkpoint: %v", err)
	}
	fmt.Printf("-- checkpoint written to %s --\n", checkpointPath)
	data, _ := os.ReadFile(checkpointPath)
	fmt.Println(string(data))
	fmt.Println("run `go run ./cmd/demo` again to simulate a restart")
}

func recoverFromCheckpoint() {
	fmt.Println("== phase 2: restarted process (fresh memory) ==")
	a := buildApp()

	fmt.Println("-- incremental hydration from checkpoint --")
	if err := a.store.LoadCheckpoint(checkpointPath); err != nil {
		log.Fatalf("load checkpoint: %v", err)
	}

	fmt.Println("-- final state --")
	fmt.Printf("  persisted counter = %d (restored)\n", a.counter.Peek())
	fmt.Printf("  persisted user    = %q (restored)\n", a.user.Peek())
	fmt.Printf("  derived  doubled  = %d (re-aligned by dependency chain)\n", a.doubled.Peek())
	fmt.Printf("  transient session = %q (safely reset)\n", a.session.Peek())

	if err := os.Remove(checkpointPath); err != nil {
		log.Fatalf("cleanup: %v", err)
	}
	fmt.Println("-- checkpoint consumed, demo reset --")
}
