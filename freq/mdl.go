package freq

import "math"

// MDL cost model (all costs in bits, fully explicit):
//
//   total = HeaderFixedBits                     (format magic, version,
//         + context->group assignment cost        flags, length, checksum)
//         + per-group parameter cost
//         + data cost under add-k smoothing
//
// The context->group assignment costs nctx*log2(numGroups) bits, so a
// fully merged (order-0) structure pays 0 assignment bits, and a fully
// split (order-1) structure pays the most. Each group's count table
// costs alpha*ParamBitsPerCount bits. Zero-frequency symbols contribute
// 0 to the data-cost sum (no log0); their smoothed probability is still
// positive, so unknown symbols remain codable (explicit escape).
const (
	// HeaderFixedBits accounts for the stream header: 4 magic/version
	// bytes, 1 flags byte, and bounded varints for alphabet size,
	// symbol count and group count, plus the 32-bit checksum.
	HeaderFixedBits = 4*8 + 8 + 3*16 + 32
	// ParamBitsPerCount is the cost of describing one stored count in
	// a group's model header (16-bit counts are sufficient for the
	// small domains this package targets).
	ParamBitsPerCount = 16
	// MaxExhaustiveContexts bounds the exhaustive structure search:
	// the number of set partitions of n contexts is the Bell number
	// (8 -> 4140), so exhaustive search is only offered for n <= 8.
	MaxExhaustiveContexts = 8
)

// StructureCost evaluates one candidate structure. hist[ctx][sym] holds
// the count of sym observed in context ctx (ctx in [0,nctx), sym in
// [0,alpha)). assign[ctx] gives the group id of each context; contexts
// sharing a group id share one merged count table. It returns the model
// (header) cost and the data cost separately.
func StructureCost(hist [][]uint64, assign []int, alpha int, K float64) (modelBits, dataBits float64) {
	nctx := len(hist)
	groups := make(map[int][]uint64) // group id -> merged [alpha] counts
	for ctx := 0; ctx < nctx; ctx++ {
		g := assign[ctx]
		merged := groups[g]
		if merged == nil {
			merged = make([]uint64, alpha)
			groups[g] = merged
		}
		for s := 0; s < alpha; s++ {
			merged[s] += hist[ctx][s]
		}
	}

	modelBits = HeaderFixedBits
	numGroups := len(groups)
	if numGroups > 1 {
		// Explicit cost of telling the decoder which group each of the
		// nctx contexts belongs to.
		modelBits += float64(nctx) * math.Log2(float64(numGroups))
	}
	for _, merged := range groups {
		modelBits += float64(alpha) * ParamBitsPerCount
		var tot uint64
		for s := 0; s < alpha; s++ {
			tot += merged[s]
		}
		denom := float64(tot) + K*float64(alpha)
		for s := 0; s < alpha; s++ {
			c := merged[s]
			if c == 0 {
				continue // zero-frequency: contributes 0, never log0
			}
			p := (float64(c) + K) / denom
			dataBits += float64(c) * -math.Log2(p)
		}
	}
	return modelBits, dataBits
}

// SelectExhaustive enumerates every set partition of the nctx contexts
// and returns the assignment with the minimum total MDL cost (model
// header cost included). nctx must be <= MaxExhaustiveContexts.
// This is the small-domain reference: cumulative statistics fed in via
// hist must equal a full recompute from the raw records.
func SelectExhaustive(hist [][]uint64, alpha int, K float64) (best []int, modelBits, dataBits float64) {
	nctx := len(hist)
	if nctx == 0 {
		return nil, 0, 0
	}
	if nctx > MaxExhaustiveContexts {
		panic("freq: SelectExhaustive called with too many contexts")
	}
	bestCost := math.Inf(1)
	bestModel, bestData := 0.0, 0.0
	assign := make([]int, nctx)
	var walk func(i, groups int)
	walk = func(i, groups int) {
		if i == nctx {
			m, d := StructureCost(hist, assign, alpha, K)
			if m+d < bestCost {
				bestCost = m + d
				bestModel, bestData = m, d
				best = append([]int(nil), assign...)
			}
			return
		}
		for g := 0; g <= groups; g++ {
			assign[i] = g
			ng := groups
			if g == groups {
				ng = groups + 1
			}
			walk(i+1, ng)
		}
	}
	assign[0] = 0
	walk(1, 1)
	return best, bestModel, bestData
}

// HistFromRecords builds the [ctx][sym] histogram from a record
// sequence. It is the full-recompute reference that incremental
// statistics are checked against.
func HistFromRecords(ctxs, syms []byte, nctx, alpha int) [][]uint64 {
	hist := make([][]uint64, nctx)
	for i := range hist {
		hist[i] = make([]uint64, alpha)
	}
	for i := range syms {
		hist[int(ctxs[i])][int(syms[i])]++
	}
	return hist
}
