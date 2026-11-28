package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	zerobubble "prescreen.local/ps-53e209913c"
)

func main() {
	var (
		p       = flag.Int("p", 4, "pipeline stages")
		m       = flag.Int("m", 8, "microbatches")
		tf      = flag.Float64("tf", 1, "forward time")
		tb      = flag.Float64("tb", 1, "activation-gradient time")
		tw      = flag.Float64("tw", 1, "weight-gradient time")
		tcomm   = flag.Float64("tcomm", 0.1, "one-way P2P latency")
		memF    = flag.Int64("memf", 3, "activation memory")
		memW    = flag.Int64("memw", 1, "retained weight-gradient memory")
		limits  = flag.String("limits", "12", "comma-separated per-stage limits or one value")
		compact = flag.Bool("compact", false, "print only comparison and audit summary")
	)
	flag.Parse()

	memLimits, err := parseInt64List(*limits)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	cfg := zerobubble.Config{
		P: *p, M: *m,
		TF: *tf, TB: *tb, TW: *tw, TComm: *tcomm,
		MemF: *memF, MemW: *memW,
		MemoryLimits: memLimits,
	}
	cmp, err := zerobubble.Compare(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "schedule error:", err)
		os.Exit(1)
	}

	if !*compact {
		fmt.Println("=== 1F1B baseline ===")
		fmt.Print(cmp.Baseline.Gantt())
		fmt.Println()
		fmt.Println("=== Zero-Bubble ===")
		fmt.Print(cmp.ZeroBubble.Gantt())
		fmt.Println()
	}
	fmt.Println("=== Comparison ===")
	fmt.Print(cmp.Report())
	printAudit("1F1B", cmp.Baseline)
	printAudit("Zero-Bubble", cmp.ZeroBubble)
	if !cmp.StrictlyBetter() {
		fmt.Fprintln(os.Stderr, "warning: this configuration does not show strict Zero-Bubble improvement")
	}
}

func parseInt64List(s string) ([]int64, error) {
	parts := strings.Split(s, ",")
	out := make([]int64, 0, len(parts))
	for _, raw := range parts {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid memory limit %q: %w", raw, err)
		}
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no memory limits supplied")
	}
	return out, nil
}

func printAudit(name string, s *zerobubble.Schedule) {
	r := zerobubble.Audit(s)
	fmt.Printf("%s audit: ok=%t deps=%t mem=%t comm=%t dag=%t deadlockFree=%t nodes=%d edges=%d\n",
		name, r.OK, r.DependencyOK, r.MemoryOK, r.CommunicationOK,
		r.AcyclicOK, r.DeadlockFree, r.GraphNodes, r.GraphEdges)
	if !r.OK {
		fmt.Println(strings.Join(r.Issues, "\n"))
	}
}
