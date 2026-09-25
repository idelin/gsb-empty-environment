// Command demo walks through the lifecycle of an applog: append across
// segments, two consumers acking at different speeds, a blocked reclaim,
// consumer removal advancing the watermark, and reopen recovery.
//
// Usage: go run ./cmd/demo [data-dir]
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"applog"
)

func main() {
	dir := filepath.Join(os.TempDir(), "applog-demo")
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	// Fresh directory so the sample output is deterministic.
	_ = os.RemoveAll(dir)

	l, err := applog.Open(dir, &applog.Options{SegmentMaxRecords: 4})
	must(err)
	fmt.Printf("1) opened log in %s (4 records/segment)\n", dir)

	for i := 0; i < 12; i++ {
		off, err := l.Append([]byte(fmt.Sprintf("msg-%02d", i)))
		must(err)
		fmt.Printf("2) append %-8s -> offset %d\n", fmt.Sprintf("msg-%02d", i), off)
	}
	fmt.Printf("3) high watermark NextOffset=%d (3 segments: 0..3 4..7 8..11)\n", l.NextOffset())

	must(l.RegisterConsumer("fast", 0))
	must(l.RegisterConsumer("slow", 0))
	must(l.Ack("fast", 12)) // fast consumed everything
	must(l.Ack("slow", 3))  // slow consumed offsets 0,1,2
	fmt.Printf("4) acks: fast=12 slow=3 -> watermark=%d (slowest consumer)\n", l.Watermark())

	n, err := l.Reclaim()
	must(err)
	fmt.Printf("5) reclaim removed %d segment(s): slow consumer still pins segment 0 (ends at offset 4)\n", n)

	must(l.RemoveConsumer("slow"))
	fmt.Printf("6) removed slow consumer -> watermark=%d\n", l.Watermark())
	n, err = l.Reclaim()
	must(err)
	fmt.Printf("7) reclaim removed %d segment(s); FirstOffset=%d, active segment kept\n", n, l.FirstOffset())

	if _, err := l.Read(0); errors.Is(err, applog.ErrOffsetReclaimed) {
		fmt.Println("8) Read(0)  -> ErrOffsetReclaimed (segment deleted)")
	} else {
		must(err)
	}
	p, err := l.Read(11)
	must(err)
	fmt.Printf("9) Read(11) -> %q (live record still readable)\n", p)

	must(l.Close())
	l2, err := applog.Open(dir, &applog.Options{SegmentMaxRecords: 4})
	must(err)
	defer l2.Close()
	pos, err := l2.Position("fast")
	must(err)
	fmt.Printf("10) reopen: NextOffset=%d FirstOffset=%d fast=%d (cursors durable)\n",
		l2.NextOffset(), l2.FirstOffset(), pos)
	off, err := l2.Append([]byte("msg-12"))
	must(err)
	fmt.Printf("11) append after reopen -> offset %d\n", off)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "demo:", err)
		os.Exit(1)
	}
}
