package decomp

import "fmt"

// Partition is a set partition of {0..n-1} in canonical form: each block
// is sorted ascending and blocks are ordered by their smallest element
// (canonical block-numbering order).
type Partition struct {
	Blocks [][]int
}

// NumBlocks returns the number of blocks.
func (p Partition) NumBlocks() int { return len(p.Blocks) }

// RGS returns the restricted growth string: rgs[v] is the index of the
// block containing variable v. A partition is in canonical form iff its
// RGS satisfies rgs[0] == 0 and rgs[i] <= 1+max(rgs[0..i-1]).
func (p Partition) RGS() []int {
	n := 0
	for _, b := range p.Blocks {
		for _, v := range b {
			if v+1 > n {
				n = v + 1
			}
		}
	}
	rgs := make([]int, n)
	for bi, b := range p.Blocks {
		for _, v := range b {
			rgs[v] = bi
		}
	}
	return rgs
}

func (p Partition) String() string {
	s := ""
	for i, b := range p.Blocks {
		if i > 0 {
			s += " | "
		}
		s += "{"
		for j, v := range b {
			if j > 0 {
				s += ","
			}
			s += fmt.Sprintf("X%d", v)
		}
		s += "}"
	}
	return s
}

// AllPartitions enumerates every set partition of {0..n-1} in
// lexicographic RGS order, i.e. canonical block-numbering order. The
// count is the Bell number B(n); B(6) = 203.
func AllPartitions(n int) []Partition {
	if n <= 0 {
		return nil
	}
	var out []Partition
	rgs := make([]int, n)
	var rec func(i, blocks int)
	rec = func(i, blocks int) {
		if i == n {
			out = append(out, partitionFromRGS(rgs, blocks))
			return
		}
		for b := 0; b <= blocks; b++ {
			rgs[i] = b
			next := blocks
			if b == blocks {
				next = blocks + 1
			}
			rec(i+1, next)
		}
	}
	rgs[0] = 0
	rec(1, 1)
	return out
}

func partitionFromRGS(rgs []int, blocks int) Partition {
	p := Partition{Blocks: make([][]int, blocks)}
	for i := range p.Blocks {
		p.Blocks[i] = []int{}
	}
	for v, b := range rgs {
		p.Blocks[b] = append(p.Blocks[b], v)
	}
	return p
}
