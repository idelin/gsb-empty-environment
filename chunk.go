package llex

import (
	"errors"
	"fmt"
	"math"
)

// ChunkConfig controls the chunked encoder.
type ChunkConfig struct {
	L              int // maximum code length per block, 1..MaxCodecL
	MaxBlocks      int // maximum number of blocks (boundaries + 1)
	MaxBlockTokens int // maximum number of symbols per block
	// EvalBudget caps how many distinct candidate blocks get an exact
	// cost evaluation (a package-merge run). <= 0 means unlimited.
	// A feasible cover of the input is always evaluated first and does
	// not consume the budget, so a feasible solution is always retained.
	EvalBudget int
}

// ChunkResult describes one EncodeChunked run.
type ChunkResult struct {
	File        []byte // the complete encoded file
	Blocks      int
	Bytes       int  // total file bytes
	Optimal     bool // true only if the layout is proven globally optimal
	Feasible    int  // total bytes of the best feasible layout found
	LowerBound  int  // reliable lower bound on the optimum (valid always)
	Evaluations int  // exact block evaluations performed
}

// per-block contribution to the file beyond its own bytes: its 16-byte
// index entry. The DP optimizes the true total file size:
//
//	fileBytes = fileFixedLen + sum_blocks (indexEntryLen + blockBytes)
const blockFileOverhead = indexEntryLen

type blockEval struct {
	table *Table
	bits  uint64
	bytes int // block bytes only (header + count + data)
	ok    bool
}

type chunkEncoder struct {
	alphabet  []string
	tokens    []uint32
	cfg       ChunkConfig
	pc        [][]uint64 // pc[i][s] = occurrences of symbol s in tokens[:i]
	cache     map[[2]int]*blockEval
	evals     int
	exhausted bool
}

// EncodeChunked encodes tokens (indices into alphabet) as a chunked file.
//
// Block boundaries may fall between any two tokens. Every block gets its
// own optimal length-limited table (SolveLengths), and the layout is
// chosen by an exact dynamic program over all legal partitions, minimizing
// the true total file size (index + table headers + data bits + alignment
// padding). This is a global optimum over candidate boundaries, never a
// concatenation of locally greedy blocks.
//
// If EvalBudget is too small to evaluate every candidate block, the
// result keeps the best feasible layout found and a reliable lower bound
// (per-block entropy bound, see blockLowerBound), and Optimal is false.
func EncodeChunked(alphabet []string, tokens []uint32, cfg ChunkConfig) (*ChunkResult, error) {
	if cfg.L < 1 || cfg.L > MaxCodecL {
		return nil, fmt.Errorf("llex: L=%d outside [1,%d]", cfg.L, MaxCodecL)
	}
	if cfg.MaxBlocks < 1 {
		return nil, errors.New("llex: MaxBlocks must be >= 1")
	}
	if cfg.MaxBlockTokens < 1 {
		return nil, errors.New("llex: MaxBlockTokens must be >= 1")
	}
	seen := make(map[string]bool, len(alphabet))
	for _, id := range alphabet {
		if seen[id] {
			return nil, fmt.Errorf("llex: duplicate alphabet ID %q", id)
		}
		seen[id] = true
	}
	for i, tok := range tokens {
		if int(tok) >= len(alphabet) {
			return nil, fmt.Errorf("llex: token %d at position %d outside alphabet of %d", tok, i, len(alphabet))
		}
	}

	n := len(tokens)
	if n == 0 {
		return &ChunkResult{
			File:       marshalFile(nil, nil),
			Optimal:    true,
			Feasible:   fileFixedLen,
			LowerBound: fileFixedLen,
		}, nil
	}
	if int64(cfg.MaxBlocks)*int64(cfg.MaxBlockTokens) < int64(n) {
		return nil, fmt.Errorf("llex: cannot cover %d tokens with %d blocks of at most %d tokens",
			n, cfg.MaxBlocks, cfg.MaxBlockTokens)
	}

	e := &chunkEncoder{
		alphabet: alphabet,
		tokens:   tokens,
		cfg:      cfg,
		pc:       make([][]uint64, n+1),
		cache:    make(map[[2]int]*blockEval),
	}
	e.pc[0] = make([]uint64, len(alphabet))
	for i, tok := range tokens {
		row := make([]uint64, len(alphabet))
		copy(row, e.pc[i])
		row[tok]++
		e.pc[i+1] = row
	}

	// A feasible cover is always evaluated first, outside the budget, so
	// the result always contains a valid file.
	e.evalFallbackCover()

	const inf = int64(math.MaxInt64 / 4)
	K, M := cfg.MaxBlocks, cfg.MaxBlockTokens
	ex := make([][]int64, K+1)
	lb := make([][]int64, K+1)
	par := make([][]int, K+1)
	for k := 0; k <= K; k++ {
		ex[k] = make([]int64, n+1)
		lb[k] = make([]int64, n+1)
		par[k] = make([]int, n+1)
		for j := range ex[k] {
			ex[k][j] = inf
			lb[k][j] = inf
			par[k][j] = -1
		}
	}
	ex[0][0] = 0
	lb[0][0] = 0

	for k := 1; k <= K; k++ {
		for j := 1; j <= n; j++ {
			lo := j - M
			if lo < 0 {
				lo = 0
			}
			for i := lo; i < j; i++ {
				var cEx, cLB int64
				if ev := e.evalBudgeted(i, j); ev != nil {
					if ev.ok {
						cEx = int64(ev.bytes) + blockFileOverhead
						cLB = cEx
					} else {
						cEx, cLB = inf, inf
					}
				} else {
					cEx = inf
					cLB = e.blockLowerBound(i, j)
				}
				if cEx != inf && ex[k-1][i] != inf && ex[k-1][i]+cEx < ex[k][j] {
					ex[k][j] = ex[k-1][i] + cEx
					par[k][j] = i
				}
				if cLB != inf && lb[k-1][i] != inf && lb[k-1][i]+cLB < lb[k][j] {
					lb[k][j] = lb[k-1][i] + cLB
				}
			}
		}
	}

	bestK, best := -1, inf
	lbBest := inf
	for k := 1; k <= K; k++ {
		if ex[k][n] < best {
			best, bestK = ex[k][n], k
		}
		if lb[k][n] < lbBest {
			lbBest = lb[k][n]
		}
	}
	if bestK < 0 {
		return nil, errors.New("llex: no feasible chunk layout found")
	}

	// Reconstruct the exact-DP-optimal feasible layout.
	type span struct{ i, j int }
	spans := make([]span, 0, bestK)
	for k, j := bestK, n; j > 0; k-- {
		i := par[k][j]
		if i < 0 {
			return nil, errors.New("llex: internal error reconstructing layout")
		}
		spans = append(spans, span{i, j})
		j = i
	}
	for l, r := 0, len(spans)-1; l < r; l, r = l+1, r-1 {
		spans[l], spans[r] = spans[r], spans[l]
	}

	blocks := make([][]byte, 0, len(spans))
	counts := make([]uint32, 0, len(spans))
	for _, s := range spans {
		ev := e.cache[[2]int{s.i, s.j}]
		if ev == nil || !ev.ok {
			return nil, errors.New("llex: internal error: unevaluated block on chosen path")
		}
		local := make([]uint32, s.j-s.i)
		for p := s.i; p < s.j; p++ {
			local[p-s.i] = uint32(indexOf(ev.table.IDs, alphabet[tokens[p]]))
		}
		blk, err := MarshalBlock(ev.table, local)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, blk)
		counts = append(counts, uint32(s.j-s.i))
	}
	file := marshalFile(blocks, counts)

	res := &ChunkResult{
		File:        file,
		Blocks:      len(blocks),
		Bytes:       len(file),
		Optimal:     !e.exhausted,
		Feasible:    fileFixedLen + int(best),
		LowerBound:  fileFixedLen + int(lbBest),
		Evaluations: e.evals,
	}
	return res, nil
}

// evalFallbackCover evaluates a feasible cover of the whole input so the
// DP always has at least one finite-cost path. Largest blocks are tried
// first; a block that cannot be coded within L bits is shrunk. Unit
// blocks are always feasible. These evaluations do not consume the
// search budget.
func (e *chunkEncoder) evalFallbackCover() [][2]int {
	n := len(e.tokens)
	var edges [][2]int
	for pos := 0; pos < n; {
		placed := false
		for j := minInt(n, pos+e.cfg.MaxBlockTokens); j > pos; j-- {
			if ev := e.evalForced(pos, j); ev.ok {
				edges = append(edges, [2]int{pos, j})
				pos = j
				placed = true
				break
			}
		}
		if !placed { // unreachable: a unit block always has 1 <= 2^L codewords
			e.evalForced(pos, pos+1)
			edges = append(edges, [2]int{pos, pos + 1})
			pos++
		}
	}
	return edges
}

// evalForced evaluates a block exactly, ignoring the budget.
func (e *chunkEncoder) evalForced(i, j int) *blockEval {
	key := [2]int{i, j}
	if ev, ok := e.cache[key]; ok {
		return ev
	}
	ev := e.computeBlock(i, j)
	e.cache[key] = ev
	return ev
}

// evalBudgeted evaluates a block exactly if the budget allows, else
// returns nil and marks the search as not provably optimal.
func (e *chunkEncoder) evalBudgeted(i, j int) *blockEval {
	key := [2]int{i, j}
	if ev, ok := e.cache[key]; ok {
		return ev
	}
	if e.cfg.EvalBudget > 0 && e.evals >= e.cfg.EvalBudget {
		e.exhausted = true
		return nil
	}
	e.evals++
	return e.evalForced(i, j)
}

// computeBlock builds the optimal table for tokens[i:j] and its exact
// byte cost. A block whose distinct symbols exceed 2^L is infeasible.
func (e *chunkEncoder) computeBlock(i, j int) *blockEval {
	counts := make([]uint64, len(e.alphabet))
	distinct := 0
	for s := range counts {
		if c := e.pc[j][s] - e.pc[i][s]; c > 0 {
			counts[s] = c
			distinct++
		}
	}
	if e.cfg.L < 64 && distinct > 1<<uint(e.cfg.L) {
		return &blockEval{ok: false}
	}
	syms := make([]Symbol, 0, distinct)
	for s, c := range counts {
		if c > 0 {
			syms = append(syms, Symbol{ID: e.alphabet[s], Freq: c})
		}
	}
	tab, err := SolveLengths(syms, e.cfg.L)
	if err != nil {
		return &blockEval{ok: false}
	}
	hdr, err := MarshalTable(tab)
	if err != nil {
		return &blockEval{ok: false}
	}
	var bits uint64
	for s, c := range counts {
		if c == 0 {
			continue
		}
		l := tab.Lengths[indexOf(tab.IDs, e.alphabet[s])]
		bits += c * uint64(l)
	}
	return &blockEval{
		table: tab,
		bits:  bits,
		bytes: len(hdr) + 4 + int((bits+7)/8),
		ok:    true,
	}
}

// blockLowerBound is a reliable lower bound on the file cost of a block
// covering tokens[i:j]: the exact table-header bytes and index entry,
// the 4-byte count field, and the entropy bound on the data
// (any prefix code needs at least H bits, hence at least floor(H/8)
// bytes; one extra byte is shaved off for floating-point safety).
func (e *chunkEncoder) blockLowerBound(i, j int) int64 {
	if e.cfg.L < 64 {
		distinct := 0
		for s := range e.alphabet {
			if e.pc[j][s] > e.pc[i][s] {
				distinct++
			}
		}
		if distinct > 1<<uint(e.cfg.L) {
			return math.MaxInt64 / 4 // infeasible block
		}
	}
	hdr := tableFixedLen
	var total uint64
	h := 0.0
	for s, id := range e.alphabet {
		c := e.pc[j][s] - e.pc[i][s]
		if c == 0 {
			continue
		}
		hdr += 3 + len(id)
		total += c
	}
	for s := range e.alphabet {
		c := e.pc[j][s] - e.pc[i][s]
		if c == 0 {
			continue
		}
		p := float64(c) / float64(total)
		h -= p * math.Log2(p)
	}
	dataLB := int(h*float64(total)/8) - 1
	if dataLB < 0 {
		dataLB = 0
	}
	return int64(blockFileOverhead + hdr + 4 + dataLB)
}

func indexOf(ids []string, id string) int {
	lo, hi := 0, len(ids)
	for lo < hi {
		mid := (lo + hi) / 2
		if ids[mid] < id {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
