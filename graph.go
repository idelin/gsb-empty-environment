package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Edge is a single directed arc with a non-negative integer capacity.
type Edge struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Capacity int64  `json:"capacity"`
}

// Graph is the input network. Parallel edges are allowed and their
// capacities add up for cut purposes. Self-loops never cross a cut.
type Graph struct {
	Version string   `json:"version"`
	Source  string   `json:"source"`
	Sink    string   `json:"sink"`
	Nodes   []string `json:"nodes"`
	Edges   []Edge   `json:"edges"`
}

// Validate checks structural constraints of the input network.
func (g *Graph) Validate() error {
	if g.Source == "" || g.Sink == "" {
		return fmt.Errorf("source and sink must be non-empty")
	}
	if g.Source == g.Sink {
		return fmt.Errorf("source and sink must differ")
	}
	for i, e := range g.Edges {
		if e.From == "" || e.To == "" {
			return fmt.Errorf("edge %d has empty endpoint", i)
		}
		if e.Capacity < 0 {
			return fmt.Errorf("edge %d (%s->%s) has negative capacity %d", i, e.From, e.To, e.Capacity)
		}
	}
	return nil
}

// NodeList returns the sorted union of declared nodes, edge endpoints,
// and the source/sink terminals.
func (g *Graph) NodeList() []string {
	seen := map[string]bool{g.Source: true, g.Sink: true}
	for _, n := range g.Nodes {
		seen[n] = true
	}
	for _, e := range g.Edges {
		seen[e.From] = true
		seen[e.To] = true
	}
	nodes := make([]string, 0, len(seen))
	for n := range seen {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	return nodes
}

// GraphVersion returns the explicit version if set, otherwise a
// deterministic content hash of the canonicalized network.
func (g *Graph) GraphVersion() string {
	if g.Version != "" {
		return g.Version
	}
	edgeLines := make([]string, 0, len(g.Edges))
	for _, e := range g.Edges {
		edgeLines = append(edgeLines, fmt.Sprintf("e:%s>%s:%d", e.From, e.To, e.Capacity))
	}
	sort.Strings(edgeLines)
	nodeLines := make([]string, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		nodeLines = append(nodeLines, "n:"+n)
	}
	sort.Strings(nodeLines)
	parts := append([]string{"s:" + g.Source, "t:" + g.Sink}, nodeLines...)
	parts = append(parts, edgeLines...)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])[:16]
}
