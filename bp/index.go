// Package bp implements a succinct balanced-parentheses index for ordered
// rooted trees.
//
// Encoding: every node contributes one '(' and one ')'. The tree is written
// as '(' <child1> ... <childk> ')' in preorder, so a tree of n nodes uses
// exactly 2n parentheses = 2n bits of payload. Nodes are numbered 0..n-1 in
// preorder; node v corresponds to the v-th '(' in the sequence.
//
// Index layout on top of the payload bits:
//   - superblock every SuperblockSize positions: absolute excess and absolute
//     open count at the superblock start, plus the minimum excess inside the
//     superblock relative to its start (int16).
//   - block every BlockSize positions: excess, minimum excess and open count
//     at/inside the block, all relative to the enclosing superblock (int16).
//
// All navigation (findclose, findopen, enclose, lca, ...) skips whole
// superblocks/blocks using the min-excess summaries and never scans the
// whole parenthesis string. Every operation is iterative, so arbitrarily
// deep trees cannot overflow the goroutine stack.
package bp

import (
	"errors"
	"fmt"
)

const (
	// BlockSize is the number of parenthesis positions per summary block.
	BlockSize = 512
	// SuperblockSize is the number of positions per superblock; it must be a
	// multiple of BlockSize.
	SuperblockSize = 8192
)

// superblock holds absolute values at the superblock start plus the minimum
// excess inside the superblock relative to its start. In-memory size: 12 B.
type superblock struct {
	absExcess int32 // excess (opens-closes) immediately before the superblock
	absOpens  int32 // number of '(' before the superblock
	relMin    int16 // min excess inside the superblock, relative to its start
}

// block holds values relative to the enclosing superblock. Size: 6 B.
type block struct {
	relExcess int16 // excess at block start minus excess at superblock start
	relMin    int16 // min excess inside the block, relative to the block start
	relOpens  int16 // opens before the block, relative to the superblock
}

// Index is a succinct balanced-parentheses index over one ordered rooted
// tree. It is safe for concurrent read use; Stats recording is not
// synchronized and is meant for single-threaded benchmarks.
type Index struct {
	bits   bitvec
	n      int // number of parentheses (2 * nodes)
	nodes  int
	sbs    []superblock
	blks   []block
	blkRMQ []int16 // derived per-superblock sparse tables over block minima
	sbRMQ  *rmq    // derived sparse table over superblock minima
	stats  Stats
}

// Build validates a balanced-parentheses string and constructs the index.
// The sequence must encode a single tree: balanced, never negative, and the
// excess may return to 0 only at the very end. A single node "()" is valid.
func Build(p string) (*Index, error) {
	n := len(p)
	if n == 0 {
		return nil, errors.New("bp: empty parentheses string")
	}
	if n%2 != 0 {
		return nil, fmt.Errorf("bp: odd length %d", n)
	}
	ix := &Index{n: n, nodes: n / 2}
	ix.bits = newBitvec(n)
	excess := 0
	for i := 0; i < n; i++ {
		switch p[i] {
		case '(':
			ix.bits.set(i)
			excess++
		case ')':
			excess--
		default:
			return nil, fmt.Errorf("bp: invalid character %q at position %d", p[i], i)
		}
		if excess < 0 {
			return nil, fmt.Errorf("bp: negative prefix balance at position %d", i)
		}
		if excess == 0 && i < n-1 {
			return nil, fmt.Errorf("bp: balance returns to 0 at position %d: not a single tree", i)
		}
	}
	if excess != 0 {
		return nil, fmt.Errorf("bp: unbalanced sequence, final excess %d", excess)
	}
	ix.sbs, ix.blks = ix.computeSummaries()
	ix.buildRMQ()
	return ix, nil
}

// buildRMQ derives the sparse tables over superblock and block minima.
func (ix *Index) buildRMQ() {
	mins := make([]int32, len(ix.sbs))
	for i := range mins {
		mins[i] = int32(ix.sbAbsMin(i))
	}
	ix.sbRMQ = newRMQ(mins)
	ix.buildBlockRMQ()
}

// computeSummaries derives superblock/block summaries from the raw bits.
// Block bi covers positions [bi*BlockSize, min((bi+1)*BlockSize, n)) and its
// relMin is the minimum of Excess over exactly that range, relative to the
// block start. Superblocks analogously. Used both at construction time and
// to verify serialized summaries at load time.
func (ix *Index) computeSummaries() ([]superblock, []block) {
	numSB := (ix.n + SuperblockSize - 1) / SuperblockSize
	numBlk := (ix.n + BlockSize - 1) / BlockSize
	sbs := make([]superblock, numSB)
	blks := make([]block, numBlk)
	excess, opens := 0, 0
	sbStartExcess, sbStartOpens := 0, 0
	sbMin := 0
	blkStartExcess := 0
	blkMin := 0
	for i := 0; i < ix.n; i++ {
		if i%SuperblockSize == 0 {
			if i > 0 {
				sbs[i/SuperblockSize-1].relMin = int16(sbMin - sbStartExcess)
			}
			sbs[i/SuperblockSize].absExcess = int32(excess)
			sbs[i/SuperblockSize].absOpens = int32(opens)
			sbStartExcess, sbStartOpens = excess, opens
			sbMin = excess
		}
		if i%BlockSize == 0 {
			if i > 0 {
				blks[i/BlockSize-1].relMin = int16(blkMin - blkStartExcess)
			}
			blks[i/BlockSize].relExcess = int16(excess - sbStartExcess)
			blks[i/BlockSize].relOpens = int16(opens - sbStartOpens)
			blkStartExcess = excess
			blkMin = excess
		}
		if excess < blkMin {
			blkMin = excess
		}
		if excess < sbMin {
			sbMin = excess
		}
		if ix.bits.get(i) {
			excess++
			opens++
		} else {
			excess--
		}
	}
	sbs[numSB-1].relMin = int16(sbMin - sbStartExcess)
	blks[numBlk-1].relMin = int16(blkMin - blkStartExcess)
	return sbs, blks
}

// NumNodes returns the number of nodes in the tree.
func (ix *Index) NumNodes() int { return ix.nodes }

// Len returns the number of parentheses (2 * NumNodes).
func (ix *Index) Len() int { return ix.n }

// NumBlocks returns the number of summary blocks.
func (ix *Index) NumBlocks() int { return len(ix.blks) }

// NumSuperblocks returns the number of superblocks.
func (ix *Index) NumSuperblocks() int { return len(ix.sbs) }

// String reconstructs the parenthesis string from the packed bits.
func (ix *Index) String() string {
	b := make([]byte, ix.n)
	for i := 0; i < ix.n; i++ {
		if ix.bits.get(i) {
			b[i] = '('
		} else {
			b[i] = ')'
		}
	}
	return string(b)
}

// SizeBytes reports the in-memory footprint split by structure.
func (ix *Index) SizeBytes() (bitsBytes, sbBytes, blkBytes, rmqBytes int) {
	return len(ix.bits.words) * 8, len(ix.sbs) * 12, len(ix.blks) * 6,
		ix.sbRMQ.bytes() + len(ix.blkRMQ)*2
}

// TotalBytes returns the total in-memory footprint of bits plus summaries.
func (ix *Index) TotalBytes() int {
	a, b, c, d := ix.SizeBytes()
	return a + b + c + d
}

// PayloadBytes returns the size of the raw encoding (1 bit per parenthesis).
func (ix *Index) PayloadBytes() int { return (ix.n + 7) / 8 }

// BlockSummary exposes one block summary for inspection: absolute excess at
// the block start and the absolute minimum excess inside the block.
func (ix *Index) BlockSummary(bi int) (baseExcess, absMin int) {
	base := ix.blockBaseExcess(bi)
	return base, base + int(ix.blks[bi].relMin)
}

// SuperblockSummary exposes one superblock summary for inspection.
func (ix *Index) SuperblockSummary(si int) (absExcess, absMin, absOpens int) {
	sb := &ix.sbs[si]
	return int(sb.absExcess), int(sb.absExcess) + int(sb.relMin), int(sb.absOpens)
}

// --- internal summary accessors (each counts as one block access) ---

func (ix *Index) blockBaseExcess(bi int) int {
	sb := &ix.sbs[bi*BlockSize/SuperblockSize]
	return int(sb.absExcess) + int(ix.blks[bi].relExcess)
}

func (ix *Index) blockBaseOpens(bi int) int {
	sb := &ix.sbs[bi*BlockSize/SuperblockSize]
	return int(sb.absOpens) + int(ix.blks[bi].relOpens)
}

func (ix *Index) blockAbsMin(bi int) int {
	return ix.blockBaseExcess(bi) + int(ix.blks[bi].relMin)
}

func (ix *Index) sbAbsMin(si int) int {
	return int(ix.sbs[si].absExcess) + int(ix.sbs[si].relMin)
}

// excessAcc returns Excess(i) = opens-before-i - closes-before-i, for
// 0 <= i <= n, counting touched blocks into acc.
func (ix *Index) excessAcc(i int, acc *int) int {
	opens := ix.rankAcc(i, acc)
	return 2*opens - i
}

// rankAcc counts '(' in positions [0, i).
func (ix *Index) rankAcc(i int, acc *int) int {
	if i <= 0 {
		return 0
	}
	if i > ix.n {
		i = ix.n
	}
	bi := i / BlockSize
	if bi >= len(ix.blks) {
		bi = len(ix.blks) - 1
	}
	bs := bi * BlockSize
	*acc++
	r := ix.blockBaseOpens(bi)
	// bs is a multiple of 64, so [bs, i) starts on a word boundary.
	w0, w1 := bs>>6, i>>6
	off := uint(i) & 63
	for w := w0; w < w1; w++ {
		r += bitsOnes(ix.bits.words[w])
	}
	if off > 0 {
		r += bitsOnes(ix.bits.words[w1] & ((uint64(1) << off) - 1))
	}
	return r
}

// selectAcc returns the position of the k-th '(' (0-based).
func (ix *Index) selectAcc(k int, acc *int) int {
	// Binary search: last superblock with absOpens <= k.
	lo, hi := 0, len(ix.sbs)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		*acc++
		if int(ix.sbs[mid].absOpens) <= k {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	bi0 := lo * (SuperblockSize / BlockSize)
	bi1 := bi0 + SuperblockSize/BlockSize
	if bi1 > len(ix.blks) {
		bi1 = len(ix.blks)
	}
	// Binary search: last block in the superblock with base opens <= k.
	blo, bhi := bi0, bi1-1
	for blo < bhi {
		mid := (blo + bhi + 1) / 2
		*acc++
		if ix.blockBaseOpens(mid) <= k {
			blo = mid
		} else {
			bhi = mid - 1
		}
	}
	bi := blo
	*acc++
	rem := k - ix.blockBaseOpens(bi)
	bs := bi * BlockSize
	end := bs + BlockSize
	if end > ix.n {
		end = ix.n
	}
	for j := bs; j < end; j++ {
		if ix.bits.get(j) {
			if rem == 0 {
				return j
			}
			rem--
		}
	}
	return -1 // unreachable for 0 <= k < nodes
}
