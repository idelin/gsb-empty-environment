// Package bptree implements a balanced-parentheses encoding of ordered
// rooted trees with a two-level (superblock/block) excess-summary index.
//
// Encoding layout: a tree of N nodes is a bitvector of 2N bits; node v is
// an open paren at position Select(v), its matching close is FindClose.
// Nodes are numbered 0..N-1 in preorder (rank of the open paren).
//
// Index layout on top of the bitvector:
//   - block      = 64 bits (one word): relMin, total excess, relOpen count
//   - superblock = 16 blocks (1024 bits): absolute excess, absolute min, opens
//
// All queries skip whole blocks via the summaries and never scan the full
// parentheses string. No recursion is used anywhere, so arbitrarily deep
// chains are safe.
package bptree

import (
	"errors"
	"math/bits"
)

const (
	BlockSize      = 64 // bits per block (one machine word)
	BlocksPerSuper = 16 // blocks per superblock
	SuperSize      = BlockSize * BlocksPerSuper
)

var (
	ErrEmpty          = errors.New("bptree: empty parentheses sequence")
	ErrOddLength      = errors.New("bptree: odd-length parentheses sequence")
	ErrNegativePrefix = errors.New("bptree: negative prefix balance")
	ErrUnbalanced     = errors.New("bptree: total balance is not zero")
	ErrBadCharacter   = errors.New("bptree: sequence contains a non-paren character")
	ErrTrailingBits   = errors.New("bptree: non-zero bits past the sequence length")
	ErrRange          = errors.New("bptree: node id or position out of range")
)

type superSummary struct {
	excess int64 // prefix excess E at the first bit of the superblock
	min    int64 // absolute minimum of E over all prefixes inside the superblock
	opens  int64 // number of open parens before the superblock
	total  int64 // total excess of the whole superblock
}

type blockSummary struct {
	base    int32  // E at block start relative to superblock start
	total   int16  // total excess of the block (+1/-1 per bit)
	relMin  int16  // min prefix excess inside the block, relative to block start (<=0, includes empty prefix)
	relOpen uint16 // opens before this block, relative to superblock start
}

// Index is a succinct-style index over a balanced parentheses bitvector.
type Index struct {
	n      int // number of bits = 2 * nodes
	nodes  int
	words  []uint64
	supers []superSummary
	blocks []blockSummary

	accesses int64 // instrumented block/word accesses for reporting
}

// Parse validates and indexes a parentheses string such as "(()())".
func Parse(s string) (*Index, error) {
	n := len(s)
	nw := (n + BlockSize - 1) / BlockSize
	w := make([]uint64, nw)
	for i := 0; i < n; i++ {
		switch s[i] {
		case '(':
			w[i/BlockSize] |= 1 << uint(i%BlockSize)
		case ')':
		default:
			return nil, ErrBadCharacter
		}
	}
	return Build(w, n)
}

// Build validates and indexes a bitvector of nbits balanced parentheses
// bits (1 = '(', 0 = ')'), little-endian within each word.
func Build(words []uint64, nbits int) (*Index, error) {
	if nbits <= 0 {
		return nil, ErrEmpty
	}
	if nbits%2 != 0 {
		return nil, ErrOddLength
	}
	nw := (nbits + BlockSize - 1) / BlockSize
	if len(words) < nw {
		return nil, ErrRange
	}
	w := make([]uint64, nw)
	copy(w, words)
	if r := nbits % BlockSize; r != 0 {
		if w[nw-1]>>uint(r) != 0 {
			return nil, ErrTrailingBits
		}
	}
	if err := validate(w, nbits); err != nil {
		return nil, err
	}
	ix := &Index{n: nbits, nodes: nbits / 2, words: w}
	ix.buildSummaries()
	return ix, nil
}

// validate scans the bitvector once, iteratively: prefix balance must never
// go negative and the total balance must be zero.
func validate(w []uint64, nbits int) error {
	nw := (nbits + BlockSize - 1) / BlockSize
	var bal int64
	for bi := 0; bi < nw; bi++ {
		nb := BlockSize
		if bi == nw-1 && nbits%BlockSize != 0 {
			nb = nbits % BlockSize
		}
		wv := w[bi]
		for i := 0; i < nb; i++ {
			if wv>>uint(i)&1 == 1 {
				bal++
			} else {
				bal--
			}
			if bal < 0 {
				return ErrNegativePrefix
			}
		}
	}
	if bal != 0 {
		return ErrUnbalanced
	}
	return nil
}

func (ix *Index) buildSummaries() {
	nw := len(ix.words)
	nsup := (nw + BlocksPerSuper - 1) / BlocksPerSuper
	ix.supers = make([]superSummary, nsup)
	ix.blocks = make([]blockSummary, nw)
	var absExcess, absOpens int64
	for s := 0; s < nsup; s++ {
		supExcess, supOpens := absExcess, absOpens
		supMin := absExcess
		for j := 0; j < BlocksPerSuper; j++ {
			bi := s*BlocksPerSuper + j
			if bi >= nw {
				break
			}
			nb := BlockSize
			if bi == nw-1 && ix.n%BlockSize != 0 {
				nb = ix.n % BlockSize
			}
			wv := ix.words[bi]
			var run, relMin, o int64
			for i := 0; i < nb; i++ {
				if wv>>uint(i)&1 == 1 {
					run++
					o++
				} else {
					run--
				}
				if run < relMin {
					relMin = run
				}
			}
			ix.blocks[bi] = blockSummary{
				base:    int32(absExcess - supExcess),
				total:   int16(run),
				relMin:  int16(relMin),
				relOpen: uint16(absOpens - supOpens),
			}
			if m := absExcess + relMin; m < supMin {
				supMin = m
			}
			absExcess += run
			absOpens += o
		}
		ix.supers[s] = superSummary{excess: supExcess, min: supMin, opens: supOpens, total: absExcess - supExcess}
	}
}

// --- instrumented accessors: every word/summary read counts as one block access ---

func (ix *Index) wordAt(bi int) uint64 { ix.accesses++; return ix.words[bi] }
func (ix *Index) blockAt(bi int) blockSummary {
	ix.accesses++
	return ix.blocks[bi]
}
func (ix *Index) superAt(si int) superSummary {
	ix.accesses++
	return ix.supers[si]
}

// ResetAccesses zeroes the block-access counter.
func (ix *Index) ResetAccesses() { ix.accesses = 0 }

// Accesses returns the number of block/word accesses since the last reset.
func (ix *Index) Accesses() int64 { return ix.accesses }

// NumNodes returns the number of tree nodes.
func (ix *Index) NumNodes() int { return ix.nodes }

// NumBits returns the length of the parentheses sequence (2 * NumNodes).
func (ix *Index) NumBits() int { return ix.n }

// Bit returns the i-th parenthesis bit (1 = '(', 0 = ')').
func (ix *Index) Bit(i int) int {
	return int(ix.wordAt(i/BlockSize) >> uint(i%BlockSize) & 1)
}

// prefixExcess returns E(k) = opens - closes over the first k bits.
func (ix *Index) prefixExcess(k int) int {
	if k <= 0 {
		return 0
	}
	bi := k / BlockSize
	off := k % BlockSize
	s := ix.superAt(bi / BlocksPerSuper)
	b := ix.blockAt(bi)
	e := int(s.excess) + int(b.base)
	if off > 0 {
		w := ix.wordAt(bi) & ((uint64(1) << uint(off)) - 1)
		e += 2*bits.OnesCount64(w) - off
	}
	return e
}

// Rank returns the preorder id of the node whose open paren is at position
// p (number of '(' in [0, p)).
func (ix *Index) Rank(p int) int {
	bi := p / BlockSize
	off := p % BlockSize
	s := ix.superAt(bi / BlocksPerSuper)
	b := ix.blockAt(bi)
	r := int(s.opens) + int(b.relOpen)
	if off > 0 {
		w := ix.wordAt(bi) & ((uint64(1) << uint(off)) - 1)
		r += bits.OnesCount64(w)
	}
	return r
}

// Select returns the position of the open paren of node v (v in preorder).
func (ix *Index) Select(v int) int {
	if v < 0 || v >= ix.nodes {
		return -1
	}
	// binary search superblocks for the one containing the v-th open
	lo, hi := 0, len(ix.supers)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if ix.superAt(mid).opens <= int64(v) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	k := v - int(ix.superAt(lo).opens)
	base := lo * BlocksPerSuper
	end := base + BlocksPerSuper
	if end > len(ix.blocks) {
		end = len(ix.blocks)
	}
	bi := base
	for ; bi+1 < end; bi++ {
		if int(ix.blockAt(bi+1).relOpen) > k {
			break
		}
	}
	k -= int(ix.blockAt(bi).relOpen)
	w := ix.wordAt(bi)
	// select the k-th set bit within the word, byte by byte
	for i := 0; i < 8; i++ {
		c := bits.OnesCount8(uint8(w >> uint(8*i)))
		if k < c {
			for j := 0; j < 8; j++ {
				if w>>uint(8*i+j)&1 == 1 {
					if k == 0 {
						return bi*BlockSize + 8*i + j
					}
					k--
				}
			}
		}
		k -= c
	}
	return -1
}

// FindClosePos returns the position matching the open paren at p.
// It skips blocks whose minimum excess stays above the target.
func (ix *Index) FindClosePos(p int) int {
	target := ix.prefixExcess(p)
	pos := p + 1
	cur := ix.prefixExcess(pos)
	for pos < ix.n {
		bi := pos / BlockSize
		off := pos % BlockSize
		if off == 0 {
			if pos%SuperSize == 0 && pos+SuperSize <= ix.n {
				if s := ix.superAt(pos / SuperSize); int(s.min) > target {
					cur += int(s.total)
					pos += SuperSize
					continue
				}
			}
			b := ix.blockAt(bi)
			s := ix.superAt(bi / BlocksPerSuper)
			if int(s.excess)+int(b.base)+int(b.relMin) > target {
				cur += int(b.total)
				pos += BlockSize
				continue
			}
		}
		w := ix.wordAt(bi)
		for i := off; i < BlockSize && pos < ix.n; i++ {
			if w>>uint(i)&1 == 1 {
				cur++
			} else {
				cur--
			}
			if cur == target {
				return pos
			}
			pos++
		}
	}
	return -1
}

// bwdSearch returns the largest k <= p with E(k) == target. Callers must
// guarantee such a k exists.
func (ix *Index) bwdSearch(p, target int) int {
	cur := ix.prefixExcess(p + 1)
	pos := p
	for pos >= 0 {
		bi := pos / BlockSize
		off := pos % BlockSize
		if off == BlockSize-1 {
			if (pos+1)%SuperSize == 0 && pos+1-SuperSize >= 0 {
				if s := ix.superAt(pos / SuperSize); int(s.min) > target {
					cur -= int(s.total)
					pos -= SuperSize
					continue
				}
			}
			b := ix.blockAt(bi)
			s := ix.superAt(bi / BlocksPerSuper)
			if int(s.excess)+int(b.base)+int(b.relMin) > target {
				cur -= int(b.total)
				pos -= BlockSize
				continue
			}
		}
		w := ix.wordAt(bi)
		s0 := bi * BlockSize
		for i := off; i >= 0; i-- {
			if w>>uint(i)&1 == 1 {
				cur--
			} else {
				cur++
			}
			if cur == target {
				return s0 + i
			}
		}
		pos = s0 - 1
	}
	return -1
}

// FindOpenPos returns the position matching the close paren at q.
func (ix *Index) FindOpenPos(q int) int {
	return ix.bwdSearch(q-1, ix.prefixExcess(q+1))
}

// EnclosePos returns the open-paren position of the parent of the node
// whose open paren is at p, or -1 if p is the root.
func (ix *Index) EnclosePos(p int) int {
	target := ix.prefixExcess(p) - 1
	if target < 0 {
		return -1
	}
	return ix.bwdSearch(p-1, target)
}

// minExcess returns the minimum of E(k) for k in [l, r], skipping whole
// blocks via the summaries.
func (ix *Index) minExcess(l, r int) int {
	cur := ix.prefixExcess(l)
	best := cur
	pos := l
	for pos < r {
		bi := pos / BlockSize
		off := pos % BlockSize
		if off == 0 && pos%SuperSize == 0 && pos+SuperSize <= r {
			s := ix.superAt(pos / SuperSize)
			if int(s.min) < best {
				best = int(s.min)
			}
			cur += int(s.total)
			pos += SuperSize
			continue
		}
		if off == 0 && pos+BlockSize <= r {
			b := ix.blockAt(bi)
			s := ix.superAt(bi / BlocksPerSuper)
			if m := int(s.excess) + int(b.base) + int(b.relMin); m < best {
				best = m
			}
			cur += int(b.total)
			pos += BlockSize
			continue
		}
		if ix.wordAt(bi)>>uint(off)&1 == 1 {
			cur++
		} else {
			cur--
		}
		if cur < best {
			best = cur
		}
		pos++
	}
	return best
}

// --- node-level navigation (nodes identified by preorder id) ---

// Depth returns the depth of node v (root = 0).
func (ix *Index) Depth(v int) int { return ix.prefixExcess(ix.Select(v)) }

// Parent returns the parent of v, or -1 for the root.
func (ix *Index) Parent(v int) int {
	p := ix.Select(v)
	e := ix.EnclosePos(p)
	if e < 0 {
		return -1
	}
	return ix.Rank(e)
}

// FirstChild returns the first child of v, or -1 for a leaf.
func (ix *Index) FirstChild(v int) int {
	p := ix.Select(v)
	if p+1 < ix.n && ix.Bit(p+1) == 1 {
		return ix.Rank(p + 1)
	}
	return -1
}

// NextSibling returns the next sibling of v, or -1.
func (ix *Index) NextSibling(v int) int {
	c := ix.FindClosePos(ix.Select(v))
	if c+1 < ix.n && ix.Bit(c+1) == 1 {
		return ix.Rank(c + 1)
	}
	return -1
}

// PrevSibling returns the previous sibling of v, or -1.
func (ix *Index) PrevSibling(v int) int {
	p := ix.Select(v)
	if p > 0 && ix.Bit(p-1) == 0 {
		return ix.Rank(ix.FindOpenPos(p - 1))
	}
	return -1
}

// Degree returns the number of children of v.
func (ix *Index) Degree(v int) int {
	d := 0
	for c := ix.FirstChild(v); c >= 0; c = ix.NextSibling(c) {
		d++
	}
	return d
}

// Child returns the k-th child (0-based) of v, or -1.
func (ix *Index) Child(v, k int) int {
	c := ix.FirstChild(v)
	for ; k > 0 && c >= 0; k-- {
		c = ix.NextSibling(c)
	}
	return c
}

// SubtreeInterval returns the [open, close] positions delimiting the
// subtree of v in the parentheses sequence.
func (ix *Index) SubtreeInterval(v int) (int, int) {
	o := ix.Select(v)
	return o, ix.FindClosePos(o)
}

// SubtreeSize returns the number of nodes in the subtree rooted at v.
func (ix *Index) SubtreeSize(v int) int {
	o, c := ix.SubtreeInterval(v)
	return (c - o + 1) / 2
}

// LCA returns the lowest common ancestor of u and v.
func (ix *Index) LCA(u, v int) int {
	if u == v {
		return u
	}
	a, b := ix.Select(u), ix.Select(v)
	if a > b {
		a, b = b, a
		u, v = v, u
	}
	if ix.FindClosePos(a) > b { // u is an ancestor of v
		return u
	}
	m := ix.minExcess(a+1, b)
	j := ix.bwdSearch(a, m-1)
	return ix.Rank(j)
}
