package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	var (
		input    = flag.String("i", "", "input file (default: stdin)")
		maxNodes = flag.Int64("budget", 0, "node-expansion budget; 0 = unlimited")
		timeMs   = flag.Int64("time", 0, "time budget in milliseconds; 0 = unlimited")
		brute    = flag.Bool("brute", false, "also run exhaustive subset enumeration (n<=24) and compare")
	)
	flag.Parse()

	var r io.Reader = os.Stdin
	if *input != "" {
		f, err := os.Open(*input)
		if err != nil {
			fmt.Fprintln(os.Stderr, "open input:", err)
			os.Exit(2)
		}
		defer f.Close()
		r = f
	}
	g, err := ParseGraph(r)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse:", err)
		os.Exit(2)
	}

	s := NewSolver(g, *maxNodes, *timeMs)
	res := s.Solve()

	if res.Optimal {
		fmt.Println("status: optimal")
	} else {
		fmt.Println("status: budget_exceeded")
	}
	fmt.Printf("deleted: %v\n", res.Set)
	fmt.Printf("cost: %d\n", res.Cost)
	fmt.Printf("count: %d\n", len(res.Set))
	fmt.Printf("lower_bound: %d\n", res.Lower)
	fmt.Printf("upper_bound: %d\n", res.Upper)
	fmt.Printf("topo_order: %v\n", res.Topo)
	fmt.Printf("nodes: %d\n", res.Nodes)

	// Self-check the certificate.
	alive := make([]bool, g.N)
	for v := 0; v < g.N; v++ {
		alive[v] = true
	}
	for _, v := range res.Set {
		alive[v] = false
	}
	if !g.VerifyTopoOrder(alive, res.Topo) {
		fmt.Fprintln(os.Stderr, "internal error: invalid topological certificate")
		os.Exit(1)
	}

	if *brute {
		bc, bs, err := BruteForce(g)
		if err != nil {
			fmt.Fprintln(os.Stderr, "brute:", err)
			os.Exit(2)
		}
		fmt.Printf("brute_cost: %d\nbrute_set: %v\n", bc, bs)
		match := bc == res.Cost && len(bs) == len(res.Set)
		if match {
			for i := range bs {
				if bs[i] != res.Set[i] {
					match = false
					break
				}
			}
		}
		if res.Optimal && !match {
			fmt.Fprintln(os.Stderr, "MISMATCH between solver and brute force")
			os.Exit(1)
		}
		fmt.Println("brute_match: true")
	}
}
