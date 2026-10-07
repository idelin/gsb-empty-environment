package main

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Public preorder cut encoding (公开先序切割编码)
//
//	leaf            -> "L"
//	horizontal cut  -> "H" + 2-digit row  + enc(top child)    + enc(bottom child)
//	vertical cut    -> "V" + 2-digit col  + enc(left child)   + enc(right child)
//
// Trees are ordered by plain lexicographic byte order of the encoding string.
// ---------------------------------------------------------------------------

type rect struct {
	x0, y0, x1, y1 int
}

func (r rect) area() int { return (r.x1 - r.x0) * (r.y1 - r.y0) }

type Image struct {
	W, H int
	sum  [][]int // prefix sums, (H+1) x (W+1)
}

func NewImage(rows []string) (*Image, error) {
	if len(rows) == 0 || len(rows[0]) == 0 {
		return nil, fmt.Errorf("empty image")
	}
	w := len(rows[0])
	if w > 8 || len(rows) > 8 {
		return nil, fmt.Errorf("image too large: %dx%d (max 8x8)", w, len(rows))
	}
	im := &Image{W: w, H: len(rows)}
	im.sum = make([][]int, im.H+1)
	for i := range im.sum {
		im.sum[i] = make([]int, im.W+1)
	}
	for y, row := range rows {
		if len(row) != w {
			return nil, fmt.Errorf("ragged rows: row %d has width %d, want %d", y, len(row), w)
		}
		for x := 0; x < w; x++ {
			var v int
			switch row[x] {
			case '0':
				v = 0
			case '1':
				v = 1
			default:
				return nil, fmt.Errorf("bad pixel %q at (%d,%d)", row[x], x, y)
			}
			im.sum[y+1][x+1] = im.sum[y][x+1] + im.sum[y+1][x] - im.sum[y][x] + v
		}
	}
	return im, nil
}

func (im *Image) ones(r rect) int {
	return im.sum[r.y1][r.x1] - im.sum[r.y0][r.x1] - im.sum[r.y1][r.x0] + im.sum[r.y0][r.x0]
}

// leafErrColor returns the mis-painted pixel count and the fill color for a
// leaf: majority color wins, ties resolve to 0.
func leafErrColor(ones, area int) (err, color int) {
	zeros := area - ones
	if ones > zeros {
		return zeros, 1
	}
	return ones, 0
}

func encCut(dir byte, pos int, c1, c2 string) string {
	return fmt.Sprintf("%c%02d%s%s", dir, pos, c1, c2)
}

// ---------------------------------------------------------------------------
// Single-frame solver: min error, then fewest leaves, then smallest encoding.
// ---------------------------------------------------------------------------

type sol1 struct {
	err, leaves int
	enc         string
}

func better1(a, b sol1) bool {
	if a.err != b.err {
		return a.err < b.err
	}
	if a.leaves != b.leaves {
		return a.leaves < b.leaves
	}
	return a.enc < b.enc
}

type key1 struct {
	x0, y0, x1, y1, budget int
}

type solver1 struct {
	img  *Image
	memo map[key1]sol1
}

func newSolver1(img *Image) *solver1 {
	return &solver1{img: img, memo: make(map[key1]sol1)}
}

// best returns the optimal solution for r using at most budget leaves.
func (s *solver1) best(r rect, budget int) sol1 {
	k := key1{r.x0, r.y0, r.x1, r.y1, budget}
	if v, ok := s.memo[k]; ok {
		return v
	}
	err, _ := leafErrColor(s.img.ones(r), r.area())
	best := sol1{err: err, leaves: 1, enc: "L"}
	if budget >= 2 {
		for y := r.y0 + 1; y < r.y1; y++ { // horizontal cut at row y
			c1 := rect{r.x0, r.y0, r.x1, y}
			c2 := rect{r.x0, y, r.x1, r.y1}
			for b1 := 1; b1 <= budget-1; b1++ {
				s1 := s.best(c1, b1)
				s2 := s.best(c2, budget-b1)
				cand := sol1{s1.err + s2.err, s1.leaves + s2.leaves, encCut('H', y, s1.enc, s2.enc)}
				if better1(cand, best) {
					best = cand
				}
			}
		}
		for x := r.x0 + 1; x < r.x1; x++ { // vertical cut at col x
			c1 := rect{r.x0, r.y0, x, r.y1}
			c2 := rect{x, r.y0, r.x1, r.y1}
			for b1 := 1; b1 <= budget-1; b1++ {
				s1 := s.best(c1, b1)
				s2 := s.best(c2, budget-b1)
				cand := sol1{s1.err + s2.err, s1.leaves + s2.leaves, encCut('V', x, s1.enc, s2.enc)}
				if better1(cand, best) {
					best = cand
				}
			}
		}
	}
	s.memo[k] = best
	return best
}

// ---------------------------------------------------------------------------
// Two-frame solver: shared cut tree, per-frame leaf colors.
// Pareto frontier of (errA, errB, leaves, enc) per (rect, budget).
// ---------------------------------------------------------------------------

type pt struct {
	errA, errB, leaves int
	enc                string
}

type frontier []pt

// prune drops dominated points; among equal (errA,errB,leaves) keeps min enc.
func prune(f frontier) frontier {
	sort.Slice(f, func(i, j int) bool {
		a, b := f[i], f[j]
		if a.errA != b.errA {
			return a.errA < b.errA
		}
		if a.errB != b.errB {
			return a.errB < b.errB
		}
		if a.leaves != b.leaves {
			return a.leaves < b.leaves
		}
		return a.enc < b.enc
	})
	out := f[:0]
	for _, p := range f {
		dup := false
		dominated := false
		for _, q := range f {
			if q.errA <= p.errA && q.errB <= p.errB && q.leaves <= p.leaves {
				if q.errA < p.errA || q.errB < p.errB || q.leaves < p.leaves {
					dominated = true
					break
				}
				if q.enc < p.enc {
					dominated = true // same costs, smaller encoding exists
					break
				}
				if q.enc == p.enc {
					dup = true
				}
			}
		}
		if dominated {
			continue
		}
		if dup { // keep only the first occurrence of an identical point
			seen := false
			for _, q := range out {
				if q == p {
					seen = true
					break
				}
			}
			if seen {
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

type solver2 struct {
	a, b *Image
	memo map[key1]frontier
}

func newSolver2(a, b *Image) *solver2 {
	return &solver2{a: a, b: b, memo: make(map[key1]frontier)}
}

// front returns the Pareto frontier for r using at most budget leaves.
func (s *solver2) front(r rect, budget int) frontier {
	k := key1{r.x0, r.y0, r.x1, r.y1, budget}
	if v, ok := s.memo[k]; ok {
		return v
	}
	area := r.area()
	errA, _ := leafErrColor(s.a.ones(r), area)
	errB, _ := leafErrColor(s.b.ones(r), area)
	res := frontier{{errA: errA, errB: errB, leaves: 1, enc: "L"}}
	if budget >= 2 {
		for y := r.y0 + 1; y < r.y1; y++ {
			c1 := rect{r.x0, r.y0, r.x1, y}
			c2 := rect{r.x0, y, r.x1, r.y1}
			for b1 := 1; b1 <= budget-1; b1++ {
				f1 := s.front(c1, b1)
				f2 := s.front(c2, budget-b1)
				for _, p := range f1 {
					for _, q := range f2 {
						res = append(res, pt{
							errA: p.errA + q.errA, errB: p.errB + q.errB,
							leaves: p.leaves + q.leaves,
							enc:    encCut('H', y, p.enc, q.enc),
						})
					}
				}
			}
		}
		for x := r.x0 + 1; x < r.x1; x++ {
			c1 := rect{r.x0, r.y0, x, r.y1}
			c2 := rect{x, r.y0, r.x1, r.y1}
			for b1 := 1; b1 <= budget-1; b1++ {
				f1 := s.front(c1, b1)
				f2 := s.front(c2, budget-b1)
				for _, p := range f1 {
					for _, q := range f2 {
						res = append(res, pt{
							errA: p.errA + q.errA, errB: p.errB + q.errB,
							leaves: p.leaves + q.leaves,
							enc:    encCut('V', x, p.enc, q.enc),
						})
					}
				}
			}
		}
	}
	res = prune(res)
	s.memo[k] = res
	return res
}

// pickBest2 selects among feasible points: min total error, then fewest
// leaves, then smallest encoding. Returns ok=false when infeasible.
func pickBest2(f frontier, k, capA, capB int) (pt, bool) {
	var best pt
	ok := false
	for _, p := range f {
		if p.leaves > k || p.errA > capA || p.errB > capB {
			continue
		}
		if !ok ||
			p.errA+p.errB < best.errA+best.errB ||
			(p.errA+p.errB == best.errA+best.errB && p.leaves < best.leaves) ||
			(p.errA+p.errB == best.errA+best.errB && p.leaves == best.leaves && p.enc < best.enc) {
			best, ok = p, true
		}
	}
	return best, ok
}

// ---------------------------------------------------------------------------
// Decoding an encoding back into a tree (for pretty printing / coloring).
// ---------------------------------------------------------------------------

type node struct {
	r      rect
	leaf   bool
	dir    byte
	pos    int
	c1, c2 *node
}

func parseEnc(enc string, r rect) (*node, error) {
	n, rest, err := parseEncAt(enc, r)
	if err != nil {
		return nil, err
	}
	if rest != "" {
		return nil, fmt.Errorf("trailing garbage in encoding: %q", rest)
	}
	return n, nil
}

func parseEncAt(enc string, r rect) (*node, string, error) {
	if enc == "" {
		return nil, "", fmt.Errorf("unexpected end of encoding")
	}
	switch enc[0] {
	case 'L':
		return &node{r: r, leaf: true}, enc[1:], nil
	case 'H', 'V':
		if len(enc) < 3 {
			return nil, "", fmt.Errorf("truncated cut token: %q", enc)
		}
		pos, err := strconv.Atoi(enc[1:3])
		if err != nil {
			return nil, "", fmt.Errorf("bad cut position in %q", enc[:3])
		}
		n := &node{r: r, dir: enc[0], pos: pos}
		var r1, r2 rect
		if enc[0] == 'H' {
			if pos <= r.y0 || pos >= r.y1 {
				return nil, "", fmt.Errorf("horizontal cut %d outside (%d,%d)", pos, r.y0, r.y1)
			}
			r1 = rect{r.x0, r.y0, r.x1, pos}
			r2 = rect{r.x0, pos, r.x1, r.y1}
		} else {
			if pos <= r.x0 || pos >= r.x1 {
				return nil, "", fmt.Errorf("vertical cut %d outside (%d,%d)", pos, r.x0, r.x1)
			}
			r1 = rect{r.x0, r.y0, pos, r.y1}
			r2 = rect{pos, r.y0, r.x1, r.y1}
		}
		rest := enc[3:]
		var err2 error
		n.c1, rest, err2 = parseEncAt(rest, r1)
		if err2 != nil {
			return nil, "", err2
		}
		n.c2, rest, err2 = parseEncAt(rest, r2)
		if err2 != nil {
			return nil, "", err2
		}
		return n, rest, nil
	}
	return nil, "", fmt.Errorf("bad token %q", enc[0])
}

func countLeaves(n *node) int {
	if n.leaf {
		return 1
	}
	return countLeaves(n.c1) + countLeaves(n.c2)
}

// render paints the tree onto a fresh canvas for the given image's leaf colors.
func render(n *node, im *Image) [][]int {
	out := make([][]int, im.H)
	for i := range out {
		out[i] = make([]int, im.W)
	}
	var paint func(nd *node)
	paint = func(nd *node) {
		if nd.leaf {
			_, color := leafErrColor(im.ones(nd.r), nd.r.area())
			for y := nd.r.y0; y < nd.r.y1; y++ {
				for x := nd.r.x0; x < nd.r.x1; x++ {
					out[y][x] = color
				}
			}
			return
		}
		paint(nd.c1)
		paint(nd.c2)
	}
	paint(n)
	return out
}

func formatTree(n *node, frames ...*Image) string {
	var sb strings.Builder
	var walk func(nd *node, depth int)
	walk = func(nd *node, depth int) {
		indent := strings.Repeat("  ", depth)
		if nd.leaf {
			fmt.Fprintf(&sb, "%sleaf (%d,%d)-(%d,%d)", indent, nd.r.x0, nd.r.y0, nd.r.x1, nd.r.y1)
			for i, im := range frames {
				err, color := leafErrColor(im.ones(nd.r), nd.r.area())
				fmt.Fprintf(&sb, "  frame%d: color=%d err=%d", i, color, err)
			}
			sb.WriteByte('\n')
			return
		}
		fmt.Fprintf(&sb, "%scut %c@%d on (%d,%d)-(%d,%d)\n", indent, nd.dir, nd.pos, nd.r.x0, nd.r.y0, nd.r.x1, nd.r.y1)
		walk(nd.c1, depth+1)
		walk(nd.c2, depth+1)
	}
	walk(n, 0)
	return sb.String()
}
