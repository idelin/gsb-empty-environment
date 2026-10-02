// Command demo shows the persistent reactive store across a real process
// restart. Run "demo save" to mutate state and write a JSON checkpoint,
// then "demo restore" (a fresh process) to hydrate it and watch the
// derived Effects re-align while transient state stays reset.
package main

import (
	"fmt"
	"os"

	"reactivestore/reactive"
)

const checkpointPath = "checkpoint.json"

// register wires the state tree. It is called identically in both process
// lifetimes: persisted Signals get their durable initial values, transient
// Signals always start from scratch.
func register(st *reactive.Store) (count, user, draft *reactive.Signal, log *[]string) {
	count = st.Signal("count", 0, reactive.Persist())         // @persist
	user = st.Signal("user", "anonymous", reactive.Persist()) // @persist
	draft = st.Signal("draft", "")                            // transient

	doubled := st.Computed("doubled", func() any {
		return count.Get().(int) * 2
	})
	log = &[]string{}
	st.Effect(func() {
		*log = append(*log, fmt.Sprintf("user=%s doubled=%d",
			user.Get().(string), doubled.Get().(int)))
	})
	return count, user, draft, log
}

func main() {
	if len(os.Args) != 2 || (os.Args[1] != "save" && os.Args[1] != "restore") {
		fmt.Println("usage: demo <save|restore>")
		os.Exit(2)
	}
	if os.Args[1] == "save" {
		save()
	} else {
		restore()
	}
}

func save() {
	st := reactive.NewStore()
	count, user, draft, log := register(st)

	fmt.Println("== process #1: mutate state ==")
	count.Set(21)
	user.Set("alice")
	count.Set(42)                      // chain: count -> doubled -> effect
	draft.Set("half-typed message...") // transient: never persisted
	for _, l := range *log {
		fmt.Println("  effect:", l)
	}
	fmt.Print(st)

	if err := st.SaveCheckpointFile(checkpointPath); err != nil {
		fmt.Fprintln(os.Stderr, "save checkpoint:", err)
		os.Exit(1)
	}
	data, _ := os.ReadFile(checkpointPath)
	fmt.Printf("== wrote %s ==\n%s", checkpointPath, data)
}

func restore() {
	st := reactive.NewStore()
	count, user, draft, log := register(st)

	fmt.Println("== process #2: fresh boot, before hydration ==")
	fmt.Print(st)

	report, err := st.LoadCheckpointFile(checkpointPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load checkpoint:", err)
		os.Exit(1)
	}
	fmt.Printf("== hydrated: restored=%v skippedUnknown=%v skippedTransient=%v ==\n",
		report.Restored, report.SkippedUnknown, report.SkippedTransient)

	fmt.Println("== after hydration: derived effects re-aligned ==")
	for _, l := range *log {
		fmt.Println("  effect:", l)
	}
	fmt.Print(st)

	if count.Get().(int) != 42 || user.Get().(string) != "alice" || draft.Get().(string) != "" {
		fmt.Fprintln(os.Stderr, "FAIL: state mismatch after hydration")
		os.Exit(1)
	}
	fmt.Println("OK: persisted state restored, transient state reset, effects re-aligned")
}
