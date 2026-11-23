package envguard

import (
	"fmt"
	"strings"
)

// CharExplanation is one annotated output rune.
type CharExplanation struct {
	Offset  int64
	Rune    rune
	Content []string
	Control []string
}

// Explain annotates every rune of a short rope. It is the cross-check tool
// used in tests and host diagnostics; long ropes should be queried through
// ProvenanceAt instead.
func (g *Graph) Explain(id NodeID) ([]CharExplanation, error) {
	if g.nodes[id].len > 1024 {
		return nil, fmt.Errorf("envguard: Explain is limited to 1024 runes; use ProvenanceAt")
	}
	out := make([]CharExplanation, 0, g.nodes[id].len)
	for i := int64(0); i < g.nodes[id].len; i++ {
		info, err := g.ProvenanceAt(id, i)
		if err != nil {
			return nil, err
		}
		ce := CharExplanation{Offset: i, Rune: info.Rune}
		for _, o := range info.Content {
			ce.Content = append(ce.Content, o.Describe())
		}
		for _, o := range info.Control {
			ce.Control = append(ce.Control, o.Describe())
		}
		out = append(out, ce)
	}
	return out, nil
}

// RenderExplanation formats Explain output as a compact per-line report.
func RenderExplanation(rows []CharExplanation) string {
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "[%2d] %q content={%s} control={%s}\n",
			r.Offset, string(r.Rune),
			strings.Join(r.Content, ","),
			strings.Join(r.Control, ","))
	}
	return b.String()
}

// NormalizedPath returns the POSIX-normalized rendering of a path rope.
func (g *Graph) NormalizedPath(id NodeID) string {
	return g.normalizePath(id)
}
