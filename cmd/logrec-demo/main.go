// Command logrec-demo reassembles a sample log in small chunks,
// demonstrates a checkpoint/restore mid-stream plus a late
// correction, and prints the resulting records and telemetry.
package main

import (
	"fmt"
	"os"

	"logrec"
)

func main() {
	data, err := os.ReadFile("testdata/sample.log")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	m := logrec.NewMultiplexer(logrec.WithBudget(1 << 20))
	var emissions []logrec.Emission

	// Feed the stream in awkward 37-byte chunks; take a checkpoint
	// halfway through and resume from a restored multiplexer.
	const chunk = 37
	pos := 0
	restored := false
	for pos < len(data) {
		end := pos + chunk
		if end > len(data) {
			end = len(data)
		}
		ems, n, err := m.FeedChunk("app", 1, data[pos:end])
		if err != nil {
			fmt.Fprintln(os.Stderr, "feed:", err)
			os.Exit(1)
		}
		emissions = append(emissions, ems...)
		pos += n
		if !restored && pos >= len(data)/2 {
			restored = true
			snap, err := m.Snapshot()
			if err != nil {
				fmt.Fprintln(os.Stderr, "snapshot:", err)
				os.Exit(1)
			}
			fmt.Printf("--- checkpoint at byte %d (%d bytes), restoring ---\n", pos, len(snap))
			m, err = logrec.Restore(snap)
			if err != nil {
				fmt.Fprintln(os.Stderr, "restore:", err)
				os.Exit(1)
			}
		}
	}
	emissions = append(emissions, m.Close("app", 1)...)

	// A late correction arrives for line 1 (already emitted).
	ems, err := m.FeedLine("app", 1, 1, "2026-10-05 09:15:01 INFO server starting on :8080 (tls)")
	if err != nil {
		fmt.Fprintln(os.Stderr, "correction:", err)
		os.Exit(1)
	}
	emissions = append(emissions, ems...)

	fmt.Println("\n=== records ===")
	for _, e := range emissions {
		r := e.Record
		kind := "closed"
		if e.Revised {
			kind = "REVISED"
		}
		fmt.Printf("[%s] id=%s rev=%d lines=%d-%d reason=%s truncated=%v\n",
			kind, r.ID, r.Revision, r.StartSeq, r.EndSeq, r.Reason, r.Truncated)
		fmt.Println(indent(r.Text()))
	}

	fmt.Println("\n=== telemetry ===")
	for _, ev := range m.Telemetry().Events() {
		fmt.Println(ev)
	}
}

func indent(s string) string {
	out := ""
	for i, line := range splitLines(s) {
		if i > 0 {
			out += "\n"
		}
		out += "    " + line
	}
	return out
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	return append(lines, s[start:])
}
