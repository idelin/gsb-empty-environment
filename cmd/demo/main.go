// Command demo records a deterministic run, replays it, and shows how
// replay pinpoints divergences. Task functions, time and failures are all
// injected; the runtime never touches the real clock.
package main

import (
	"errors"
	"fmt"
	"time"

	deter "example.com/deter"
)

func worker(name string, out *[]string) deter.TaskFunc {
	return func(env *deter.Env) {
		for i := 0; i < 2; i++ {
			env.Sleep(time.Duration(i+1) * time.Second)
			res, err := env.Call("fetch", name)
			if err != nil {
				*out = append(*out, fmt.Sprintf("%s: error: %v", name, err))
			} else {
				*out = append(*out, fmt.Sprintf("%s: %s", name, res))
			}
		}
	}
}

func root(out *[]string) deter.TaskFunc {
	return func(env *deter.Env) {
		env.Spawn("worker-a", worker("a", out))
		env.Spawn("worker-b", worker("b", out))
		env.Sleep(500 * time.Millisecond)
		res, _ := env.Call("fetch", "root")
		*out = append(*out, "root: "+res)
	}
}

// handler injects external results and a failure for worker "b".
func handler(name, arg string) (string, error) {
	if arg == "b" {
		return "", fmt.Errorf("injected failure for %q", arg)
	}
	return "data-" + arg, nil
}

func main() {
	// 1. Record.
	var out []string
	rt := deter.NewRecord(handler)
	events, err := rt.Run("root", root(&out))
	if err != nil {
		panic(err)
	}
	fmt.Printf("recorded %d events, virtual clock stopped at %s\n", len(events), rt.Now())
	for i, ev := range events {
		fmt.Printf("  step %2d: %s\n", i, ev)
	}
	fmt.Println("task output:")
	for _, l := range out {
		fmt.Println("  " + l)
	}

	// 2. Replay the same recording against the same tasks.
	var out2 []string
	if _, err := deter.NewReplay(events).Run("root", root(&out2)); err != nil {
		panic(err)
	}
	same := fmt.Sprint(out) == fmt.Sprint(out2)
	fmt.Printf("\nreplay: OK, identical output: %v\n", same)

	// 3. Replay against a task that issues one extra request.
	cheated := func(env *deter.Env) {
		env.Spawn("worker-a", func(e *deter.Env) {
			e.Call("ping", "a") // extra, unrecorded request
			e.Sleep(time.Second)
		})
		env.Spawn("worker-b", worker("b", &out2))
		env.Sleep(500 * time.Millisecond)
		env.Call("fetch", "root")
	}
	_, err = deter.NewReplay(events).Run("root", cheated)
	report("extra request", err)

	// 4. Replay a recording whose tail is missing.
	truncated := events[:len(events)-3]
	_, err = deter.NewReplay(truncated).Run("root", root(&out2))
	report("truncated recording", err)
}

func report(label string, err error) {
	var div *deter.Divergence
	switch {
	case err == nil:
		fmt.Printf("%s: replay unexpectedly succeeded\n", label)
	case errors.As(err, &div):
		fmt.Printf("%s: %v\n", label, div)
	default:
		fmt.Printf("%s: error: %v\n", label, err)
	}
}
