package graphcanon

import (
	"strconv"
	"strings"
)

const hexdigits = "0123456789abcdef"

// encode renders the graph under the given numbering as a canonical-form
// candidate string. numbering[orig] = canonical position; the caller
// guarantees numbering[g.root] == 0, so the root is always at position 0
// and is not stored explicitly.
//
// The encoding is injective: two numbered, labeled multigraphs produce the
// same string iff they are identical (same size, same labels in order, same
// adjacency multiplicities). Labels are length-prefixed so arbitrary label
// bytes cannot collide with the framing, and multiplicities are fixed-width
// hex so the matrix occupies a fixed-size, order-preserving suffix.
func encode(g *Graph, numbering []int) string {
	n := g.n
	inv := make([]int, n)
	for orig, c := range numbering {
		inv[c] = orig
	}
	var b strings.Builder
	b.Grow(1 + n*8 + n*n*2)
	b.WriteByte(byte('0' + n))
	for p := 0; p < n; p++ {
		l := g.labels[inv[p]]
		b.WriteByte('|')
		b.WriteString(strconv.Itoa(len(l)))
		b.WriteByte(':')
		b.WriteString(l)
	}
	b.WriteByte('#')
	for p := 0; p < n; p++ {
		row := g.adj[inv[p]]
		for q := 0; q < n; q++ {
			m := row[inv[q]]
			b.WriteByte(hexdigits[m>>4])
			b.WriteByte(hexdigits[m&15])
		}
	}
	return b.String()
}
