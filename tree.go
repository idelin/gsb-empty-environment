package cutsolve

import (
	"fmt"
	"strconv"
	"strings"
)

// Tree is a recursive integer-line cut tree. A node is either a leaf (filled
// with its majority color) or an internal split with two children.
type Tree struct {
	Leaf bool
	Dir  byte // 'V' or 'H' (internal nodes only)
	Pos  int  // absolute coordinate of the cut line
	L, R *Tree
}

var leafNode = &Tree{Leaf: true}

// Leaf returns the shared immutable leaf node.
func Leaf() *Tree { return leafNode }

// Split builds an internal node. Children must lie on the two sides of the cut.
func Split(dir byte, pos int, l, r *Tree) *Tree {
	return &Tree{Dir: dir, Pos: pos, L: l, R: r}
}

// Leaves counts leaf nodes.
func (t *Tree) Leaves() int {
	if t.Leaf {
		return 1
	}
	return t.L.Leaves() + t.R.Leaves()
}

// Encoding is the public pre-order cut encoding:
//   - leaf            -> "L"
//   - internal node   -> Dir letter ('H'/'V') + decimal cut position,
//     then the encoding of the first child (top for 'H', left for 'V'),
//     then the encoding of the second child.
//
// Example: "V1H1LLL" splits vertically at x=1; the left child splits
// horizontally at y=1 into two leaves; the right child is a leaf.
func (t *Tree) Encoding() string {
	var sb strings.Builder
	t.encode(&sb)
	return sb.String()
}

func (t *Tree) encode(sb *strings.Builder) {
	if t.Leaf {
		sb.WriteByte('L')
		return
	}
	sb.WriteByte(t.Dir)
	sb.WriteString(strconv.Itoa(t.Pos))
	t.L.encode(sb)
	t.R.encode(sb)
}

// tokenRank orders node tokens as 'H' < 'L' < 'V' (their byte values).
func tokenRank(t *Tree) int {
	if t.Leaf {
		return 1 // 'L'
	}
	if t.Dir == 'H' {
		return 0 // 'H'
	}
	return 2 // 'V'
}

// Compare orders trees by their pre-order encoding, token by token: first by
// token kind ('H' < 'L' < 'V'), then by ascending cut position, then
// recursively by first and second child. It is a total order and matches
// lexicographic comparison of Encoding() whenever positions are single-digit
// (always true for images up to 8x8).
func Compare(a, b *Tree) int {
	ra, rb := tokenRank(a), tokenRank(b)
	if ra != rb {
		return ra - rb
	}
	if a.Leaf {
		return 0
	}
	if a.Pos != b.Pos {
		return a.Pos - b.Pos
	}
	if c := Compare(a.L, b.L); c != 0 {
		return c
	}
	return Compare(a.R, b.R)
}

// splitRect applies the node's cut to r, returning the first and second child
// rectangles.
func splitRect(r Rect, t *Tree) (Rect, Rect) {
	if t.Dir == 'V' {
		return Rect{r.X0, r.Y0, t.Pos, r.Y1}, Rect{t.Pos, r.Y0, r.X1, r.Y1}
	}
	return Rect{r.X0, r.Y0, r.X1, t.Pos}, Rect{r.X0, t.Pos, r.X1, r.Y1}
}

// FrameError sums miscolored pixels of every leaf for one frame.
func (t *Tree) FrameError(im Image, r Rect) int {
	if t.Leaf {
		_, e := im.LeafStats(r)
		return e
	}
	a, b := splitRect(r, t)
	return t.L.FrameError(im, a) + t.R.FrameError(im, b)
}

// Pretty renders the tree with absolute rectangles; for each given frame it
// annotates every leaf with its majority color and error.
func (t *Tree) Pretty(r Rect, imgs ...Image) string {
	var sb strings.Builder
	t.pretty(&sb, r, "", imgs)
	return sb.String()
}

func (t *Tree) pretty(sb *strings.Builder, r Rect, indent string, imgs []Image) {
	if t.Leaf {
		fmt.Fprintf(sb, "%sL %s", indent, r)
		for i, im := range imgs {
			c, e := im.LeafStats(r)
			fmt.Fprintf(sb, "  f%d: color=%d err=%d", i, c, e)
		}
		sb.WriteByte('\n')
		return
	}
	fmt.Fprintf(sb, "%s%c@%d %s\n", indent, t.Dir, t.Pos, r)
	a, b := splitRect(r, t)
	t.L.pretty(sb, a, indent+"  ", imgs)
	t.R.pretty(sb, b, indent+"  ", imgs)
}
