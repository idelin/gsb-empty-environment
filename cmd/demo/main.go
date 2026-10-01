// Command demo feeds a messy sensor stream (with small logical-clock
// regressions) through the stabilization window and prints the
// reordered, strictly monotonic output stream.
package main

import (
	"fmt"

	stabilizer "eventstabilizer"
)

func main() {
	const k = 3 // tolerate out-of-orderness of up to 3 logical ticks
	s := stabilizer.New(k)

	// Weakly-ordered sensor input: mostly increasing Seq, with small
	// regressions (3 arrives after 5, 6 after 8, ...).
	stream := []stabilizer.Event{
		{Seq: 1, Source: "temp-1", Value: 21.5},
		{Seq: 2, Source: "temp-2", Value: 22.0},
		{Seq: 5, Source: "hum-1", Value: 0.41},
		{Seq: 3, Source: "temp-1", Value: 21.6}, // regression
		{Seq: 4, Source: "temp-2", Value: 22.1},
		{Seq: 8, Source: "hum-1", Value: 0.43},
		{Seq: 6, Source: "temp-1", Value: 21.8}, // regression
		{Seq: 7, Source: "temp-2", Value: 22.4},
		{Seq: 10, Source: "hum-1", Value: 0.44},
		{Seq: 9, Source: "temp-1", Value: 22.2}, // regression
		{Seq: 11, Source: "temp-2", Value: 22.6},
		{Seq: 12, Source: "hum-1", Value: 0.45},
	}

	fmt.Printf("stabilization window K=%d\n", k)
	fmt.Println("--- input (arrival order) vs stabilized output ---")
	for _, e := range stream {
		fmt.Printf("push  seq=%2d %-6s -> ", e.Seq, e.Source)
		out := s.Push(e)
		if len(out) == 0 {
			fmt.Println("(buffered, watermark not yet safe)")
			continue
		}
		for _, st := range out {
			fmt.Printf("emit seq=%2d %-6s value=%.2f  ", st.Seq, st.Source, st.Value)
		}
		fmt.Println()
	}
	fmt.Println("--- stream ended, flushing remainder ---")
	for _, st := range s.Flush() {
		fmt.Printf("emit seq=%2d %-6s value=%.2f\n", st.Seq, st.Source, st.Value)
	}
	fmt.Printf("dropped=%d pending=%d\n", s.Dropped(), s.Pending())
}
