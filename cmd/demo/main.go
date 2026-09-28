// Command demo runs a small deterministic discrete-event network
// simulation, demonstrates generational-handle cancellation of a traffic
// generator, and proves that a checkpointed/restored run produces a trace
// identical to a continuous run. No real time or network I/O is used.
package main

import (
	"fmt"
	"hash/fnv"
	"strings"

	"desim"
)

const (
	cancelAt = 200
	split    = 500
	end      = 1000
)

func build() *desim.Sim {
	s := desim.NewSim(7)
	a := s.AddNode("A")
	b := s.AddNode("B")
	c := s.AddNode("C")
	s.AddLink(a, b, 2, 5, 100)         // A->B: 2 B/tick, 5-tick latency, 100 B queue
	s.AddLink(b, c, 1, 10, 50)         // B->C: 1 B/tick, 10-tick latency, 50 B queue
	s.AddGenerator(a, c, 10, 40, 7, 3) // gen 0: A->C, 10-40 B every ~7 ticks
	s.AddGenerator(b, c, 5, 15, 11, 0) // gen 1: B->C, 5-15 B every 11 ticks
	return s
}

func traceHash(trace []string) uint64 {
	h := fnv.New64a()
	for _, line := range trace {
		h.Write([]byte(line))
		h.Write([]byte("\n"))
	}
	return h.Sum64()
}

func stats(s *desim.Sim) string {
	avg := float64(0)
	if s.Delivered > 0 {
		avg = float64(s.TotalDelay) / float64(s.Delivered)
	}
	return fmt.Sprintf("delivered=%d avg-delay=%.1f ticks drops=%d trace-hash=%016x",
		s.Delivered, avg, s.Links[0].Q.Drops+s.Links[1].Q.Drops, traceHash(s.Trace))
}

func main() {
	// 1. Continuous reference run: cancel gen 1 at tick 200, run to end.
	full := build()
	full.RunUntil(cancelAt)
	full.CancelGenerator(1)
	full.RunUntil(end)

	// 2. Same run, but checkpoint at tick 500 and continue in-process.
	s := build()
	s.RunUntil(cancelAt)
	s.CancelGenerator(1)
	s.RunUntil(split)
	cp, err := s.Checkpoint()
	if err != nil {
		panic(err)
	}
	s.RunUntil(end)

	// 3. Restore from the checkpoint and run to end.
	restored, err := desim.Restore(cp)
	if err != nil {
		panic(err)
	}
	restored.RunUntil(end)

	fmt.Printf("cancelled generator 1 at tick %d; checkpoint at tick %d (%d bytes)\n\n",
		cancelAt, split, len(cp))
	fmt.Printf("continuous          : %s\n", stats(full))
	fmt.Printf("checkpoint+continue : %s\n", stats(s))
	fmt.Printf("restored+continue   : %s\n", stats(restored))

	fmt.Println()
	if traceHash(s.Trace) == traceHash(full.Trace) && traceHash(restored.Trace) == traceHash(full.Trace) {
		fmt.Println("OK: continuous, checkpointed and restored traces are identical")
	} else {
		fmt.Println("MISMATCH: trajectories diverged")
	}

	fmt.Println()
	fmt.Println("== trace lines around the cancellation (tick 200) ==")
	shown := 0
	for _, line := range full.Trace {
		if strings.Contains(line, "cancel") || (shown > 0 && shown <= 4) {
			fmt.Println(strings.TrimSpace(line))
			shown++
		}
	}
	fmt.Println()
	fmt.Println("== first 8 trace lines ==")
	for _, line := range full.Trace[:8] {
		fmt.Println(strings.TrimSpace(line))
	}
	fmt.Println("== last 5 trace lines ==")
	for _, line := range full.Trace[len(full.Trace)-5:] {
		fmt.Println(strings.TrimSpace(line))
	}
}
