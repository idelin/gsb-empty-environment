package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"sort"
)

// Edge is a directed arc with a non-negative integer capacity.
// Vertices are 0-based internally; the text format is 1-based.
type Edge struct {
	U, V int
	C    int64
}

// Graph is a directed capacitated network with a source and a sink.
type Graph struct {
	N     int
	S, T  int
	Edges []Edge
}

// ParseGraph reads "n m s t" followed by m lines "u v c".
// Vertices in the input are 1-based. Capacities must be >= 0.
func ParseGraph(r io.Reader) (*Graph, error) {
	var n, m, s, t int
	if _, err := fmt.Fscan(r, &n, &m, &s, &t); err != nil {
		return nil, fmt.Errorf("reading header (n m s t): %w", err)
	}
	if n < 2 {
		return nil, fmt.Errorf("n must be >= 2, got %d", n)
	}
	if m < 0 {
		return nil, fmt.Errorf("m must be >= 0, got %d", m)
	}
	if s < 1 || s > n || t < 1 || t > n {
		return nil, fmt.Errorf("s and t must be in [1,%d], got s=%d t=%d", n, s, t)
	}
	if s == t {
		return nil, fmt.Errorf("s and t must differ, both are %d", s)
	}
	g := &Graph{N: n, S: s - 1, T: t - 1, Edges: make([]Edge, 0, m)}
	for i := 0; i < m; i++ {
		var u, v int
		var c int64
		if _, err := fmt.Fscan(r, &u, &v, &c); err != nil {
			return nil, fmt.Errorf("reading edge %d of %d: %w", i+1, m, err)
		}
		if u < 1 || u > n || v < 1 || v > n {
			return nil, fmt.Errorf("edge %d: endpoints must be in [1,%d], got %d %d", i+1, n, u, v)
		}
		if c < 0 {
			return nil, fmt.Errorf("edge %d: capacity must be >= 0, got %d", i+1, c)
		}
		g.Edges = append(g.Edges, Edge{U: u - 1, V: v - 1, C: c})
	}
	return g, nil
}

// Version returns a stable fingerprint of the graph. Any change to the
// vertex count, source, sink, or the edge multiset changes the version.
// Cursors are bound to this version so a page token is never silently
// applied to a different graph.
func (g *Graph) Version() string {
	type arc struct {
		u, v int
		c    int64
	}
	arcs := make([]arc, len(g.Edges))
	for i, e := range g.Edges {
		arcs[i] = arc{e.U, e.V, e.C}
	}
	sort.Slice(arcs, func(i, j int) bool {
		if arcs[i].u != arcs[j].u {
			return arcs[i].u < arcs[j].u
		}
		if arcs[i].v != arcs[j].v {
			return arcs[i].v < arcs[j].v
		}
		return arcs[i].c < arcs[j].c
	})
	h := sha256.New()
	fmt.Fprintf(h, "n=%d s=%d t=%d\n", g.N, g.S, g.T)
	for _, a := range arcs {
		fmt.Fprintf(h, "%d %d %d\n", a.u, a.v, a.c)
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
