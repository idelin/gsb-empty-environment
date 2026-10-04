package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

func nodePath(g *Graph, path []int) string {
	names := make([]string, len(path))
	for i, v := range path {
		names[i] = g.Names[v]
	}
	return strings.Join(names, "->")
}

func cycleString(g *Graph, cyc []int) string {
	closed := make([]int, len(cyc)+1)
	copy(closed, cyc)
	closed[len(cyc)] = cyc[0]
	return nodePath(g, closed)
}

func main() {
	file := flag.String("f", "", "graph file (default: stdin); lines: u v w")
	srcs := flag.String("s", "", "comma-separated source nodes")
	all := flag.Bool("all", false, "use every node as a source")
	verify := flag.Bool("verify", false, "cross-check against brute force (small graphs)")
	flag.Parse()

	var r io.Reader = os.Stdin
	if *file != "" {
		f, err := os.Open(*file)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer f.Close()
		r = f
	}
	g, err := ParseGraph(r)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	var sources []int
	if *all {
		for i := 0; i < g.N(); i++ {
			sources = append(sources, i)
		}
	} else if *srcs != "" {
		for _, name := range strings.Split(*srcs, ",") {
			name = strings.TrimSpace(name)
			i, ok := g.Index[name]
			if !ok {
				fmt.Fprintf(os.Stderr, "unknown source node %q\n", name)
				os.Exit(1)
			}
			sources = append(sources, i)
		}
	} else {
		fmt.Fprintln(os.Stderr, "specify sources with -s a,b,c or use -all")
		os.Exit(1)
	}

	targets := make([]int, g.N())
	for i := range targets {
		targets[i] = i
	}
	sort.Slice(targets, func(a, b int) bool { return g.Names[targets[a]] < g.Names[targets[b]] })

	mismatches := 0
	for _, s := range sources {
		res := g.ShortestFrom(s)
		fmt.Printf("source %s:\n", g.Names[s])
		for _, t := range targets {
			r := res[t]
			name := g.Names[t]
			switch r.Status {
			case Finite:
				fmt.Printf("  %-10s dist=%d path=%s\n", name, r.Dist, nodePath(g, r.Path))
			case Unreachable:
				fmt.Printf("  %-10s unreachable\n", name)
			case NegInf:
				fmt.Printf("  %-10s -inf  cycle=%s (weight %d)  from-source=%s  to-target=%s\n",
					name, cycleString(g, r.Cycle), r.CycleWeight,
					nodePath(g, r.ToCycle), nodePath(g, r.FromCycle))
			}
		}
		if *verify {
			if g.N() > 10 {
				fmt.Println("  verify: skipped (graph too large for brute force)")
				continue
			}
			bad := 0
			for t := 0; t < g.N(); t++ {
				bs, bd := g.BruteStatus(s, t)
				if res[t].Status != bs || (bs == Finite && res[t].Dist != bd) {
					fmt.Printf("  verify MISMATCH %s->%s: got %v/%d want %v/%d\n",
						g.Names[s], g.Names[t], res[t].Status, res[t].Dist, bs, bd)
					bad++
				}
			}
			if bad == 0 {
				fmt.Printf("  verify: OK (%d targets)\n", g.N())
			}
			mismatches += bad
		}
	}
	if mismatches > 0 {
		os.Exit(2)
	}
}
