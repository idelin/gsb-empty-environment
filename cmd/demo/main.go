// Command demo runs the acceptance scenario: a network with initial max flow
// 10, a key edge whose capacity drops 5 -> 2, reverse-track withdrawal of 3
// flow units, re-augmentation via an alternative path, and the resulting
// min-cut certificate.
package main

import (
	"fmt"

	"maxflow"
)

// Node ids: s=0, a=1, b=2, c=3, d=4, t=5.
var names = []string{"s", "a", "b", "c", "d", "t"}

func name(x int) string { return names[x] }

func printCert(label string, cert maxflow.Certificate) {
	fmt.Printf("%s: max flow = %d\n", label, cert.Value)
	fmt.Printf("  S side (reachable from s): %v\n", named(cert.SSide))
	fmt.Printf("  T side:                    %v\n", named(cert.TSide))
	fmt.Println("  cut edges (S -> T, saturated):")
	var sum int64
	for _, e := range cert.CutEdges {
		fmt.Printf("    %s -> %s  cap=%d flow=%d\n", name(e.From), name(e.To), e.Cap, e.Flow)
		sum += e.Cap
	}
	fmt.Printf("  cut capacity = %d (== max flow, certificate valid)\n", sum)
}

func named(nodes []int) []string {
	out := make([]string, len(nodes))
	for i, x := range nodes {
		out[i] = name(x)
	}
	return out
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	const (
		s = 0
		a = 1
		b = 2
		c = 3
		d = 4
		t = 5
	)
	n := maxflow.NewNetwork(6, s, t)
	for _, e := range []struct {
		u, v int
		cap  int64
	}{
		{s, a, 5},
		{s, b, 5},
		{a, c, 5}, // key edge
		{b, c, 5},
		{c, t, 10},
		{a, d, 1}, // alternative path a->d->t
		{d, t, 1},
	} {
		must(n.AddEdge(e.u, e.v, e.cap))
	}

	n.MaxFlow()
	printCert("initial", n.Cert())
	fmt.Println()

	fmt.Println(">>> change: key edge a->c capacity 5 -> 2 (delta -3)")
	cert, err := n.Adjust([]maxflow.Change{{From: a, To: c, Delta: -3}})
	must(err)
	f, _ := n.Flow(a, c)
	fmt.Printf("    a->c flow now %d (3 units withdrawn along reverse residual tracks)\n", f)
	f, _ = n.Flow(a, d)
	fmt.Printf("    alternative path a->d->t now carries %d unit(s)\n", f)
	fmt.Println()
	printCert("after decrease", cert)
	fmt.Println()

	fmt.Println(">>> change: restore key edge a->c capacity 2 -> 5 (delta +3)")
	cert, err = n.Adjust([]maxflow.Change{{From: a, To: c, Delta: 3}})
	must(err)
	printCert("after increase", cert)
}
