package freq

import (
	"math"
	"sort"
)

// table is a reference-counted count table. Several contexts may share
// one table after a merge; any mutation through a context that does not
// exclusively own the table triggers a copy-on-write detach, so
// deleting or retracting through one context can never pollute another
// context that shares (or shared) the same counts.
type table struct {
	counts map[byte]uint64
	total  uint64
	refs   int
}

func (t *table) clone() *table {
	nt := &table{counts: make(map[byte]uint64, len(t.counts)), total: t.total, refs: 1}
	for s, c := range t.counts {
		nt.counts[s] = c
	}
	return nt
}

// CondModel is an order-1 conditional frequency model: the context is
// the previous symbol. Contexts can be merged (sharing one count
// table) and later split apart again.
type CondModel struct {
	ctxs  map[byte]*table
	K     float64
	Alpha int
}

// NewCondModel returns an empty conditional model for an alphabet of
// the given size, using DefaultK smoothing.
func NewCondModel(alphabetSize int) *CondModel {
	if alphabetSize < 1 {
		alphabetSize = 1
	}
	if alphabetSize > 256 {
		alphabetSize = 256
	}
	return &CondModel{ctxs: make(map[byte]*table), K: DefaultK, Alpha: alphabetSize}
}

// own returns the privately owned table for ctx, detaching it from any
// shared table first (copy-on-write).
func (cm *CondModel) own(ctx byte) *table {
	t := cm.ctxs[ctx]
	if t == nil {
		t = &table{counts: make(map[byte]uint64), refs: 1}
		cm.ctxs[ctx] = t
		return t
	}
	if t.refs > 1 {
		t.refs--
		t = t.clone()
		cm.ctxs[ctx] = t
	}
	return t
}

// Add records one occurrence of sym in context ctx.
func (cm *CondModel) Add(ctx, sym byte) {
	t := cm.own(ctx)
	t.counts[sym]++
	t.total++
}

// Retract removes one occurrence of sym in context ctx. Retracting a
// sample that is not present is an error (ErrNotPresent). If ctx
// shares its table with other contexts, the table is copied first, so
// the retraction never affects the other contexts.
func (cm *CondModel) Retract(ctx, sym byte) error {
	t := cm.ctxs[ctx]
	if t == nil || t.counts[sym] == 0 {
		return ErrNotPresent
	}
	t = cm.own(ctx)
	c := t.counts[sym]
	if c == 1 {
		delete(t.counts, sym)
	} else {
		t.counts[sym] = c - 1
	}
	t.total--
	return nil
}

// MergeContexts merges contexts a and b into a single shared count
// table holding the sum of both. Afterwards both contexts see the
// merged distribution. The merge is structural, not destructive:
// either context can later be detached with SplitContext, and
// mutations through one context copy the table instead of polluting
// the other.
func (cm *CondModel) MergeContexts(a, b byte) {
	if a == b {
		return
	}
	ta := cm.ctxs[a]
	tb := cm.ctxs[b]
	if ta == nil && tb == nil {
		return
	}
	if ta != nil && ta == tb {
		return
	}
	merged := &table{counts: make(map[byte]uint64), refs: 0}
	if ta != nil {
		for s, c := range ta.counts {
			merged.counts[s] += c
		}
		merged.total += ta.total
		ta.refs--
	}
	if tb != nil {
		for s, c := range tb.counts {
			merged.counts[s] += c
		}
		merged.total += tb.total
		tb.refs--
	}
	if ta != nil {
		merged.refs++
		cm.ctxs[a] = merged
	}
	if tb != nil {
		merged.refs++
		cm.ctxs[b] = merged
	}
}

// SplitContext detaches ctx from any shared table, giving it a private
// copy of the (possibly merged) counts. Contexts that shared the table
// keep sharing the original.
func (cm *CondModel) SplitContext(ctx byte) {
	t := cm.ctxs[ctx]
	if t == nil || t.refs == 1 {
		return
	}
	t.refs--
	cm.ctxs[ctx] = t.clone()
}

// DeleteContext removes ctx from the model. If its table is shared,
// only the reference is dropped; the other contexts keep their counts
// untouched (no pollution through shared counts).
func (cm *CondModel) DeleteContext(ctx byte) {
	t := cm.ctxs[ctx]
	if t == nil {
		return
	}
	t.refs--
	delete(cm.ctxs, ctx)
}

// Count returns the count of sym in context ctx.
func (cm *CondModel) Count(ctx, sym byte) uint64 {
	t := cm.ctxs[ctx]
	if t == nil {
		return 0
	}
	return t.counts[sym]
}

// ContextTotal returns the number of samples recorded in context ctx.
func (cm *CondModel) ContextTotal(ctx byte) uint64 {
	t := cm.ctxs[ctx]
	if t == nil {
		return 0
	}
	return t.total
}

// Contexts returns the contexts currently present, sorted.
func (cm *CondModel) Contexts() []byte {
	out := make([]byte, 0, len(cm.ctxs))
	for c := range cm.ctxs {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// uniqueTables returns each distinct table exactly once, so shared
// (merged) tables are not double counted. The order is deterministic
// (by smallest owning context) to keep floating point sums stable.
func (cm *CondModel) uniqueTables() []*table {
	seen := make(map[*table]bool)
	var out []*table
	for _, ctx := range cm.Contexts() {
		t := cm.ctxs[ctx]
		if seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// Total returns the total number of samples held, counting each shared
// table once. It always equals adds minus retracts (conservation).
func (cm *CondModel) Total() uint64 {
	var n uint64
	for _, t := range cm.uniqueTables() {
		n += t.total
	}
	return n
}

// Prob returns the add-k smoothed probability of sym in context ctx.
// It is strictly positive for every symbol in the alphabet, including
// in empty contexts (explicit escape strategy for unknown symbols).
func (cm *CondModel) Prob(ctx, sym byte) float64 {
	t := cm.ctxs[ctx]
	var c uint64
	var tot uint64
	if t != nil {
		c = t.counts[sym]
		tot = t.total
	}
	return (float64(c) + cm.K) / (float64(tot) + cm.K*float64(cm.Alpha))
}

// Entropy returns the conditional entropy estimate
// H = sum_ctx p(ctx) * H(dist|ctx) in bits per symbol. Zero-frequency
// symbols are skipped (no log0). Deterministic contexts contribute 0.
func (cm *CondModel) Entropy() float64 {
	tables := cm.uniqueTables()
	var total uint64
	for _, t := range tables {
		total += t.total
	}
	if total == 0 {
		return 0
	}
	h := 0.0
	for _, t := range tables {
		if t.total == 0 {
			continue
		}
		hc := 0.0
		tt := float64(t.total)
		// Sorted iteration keeps the summation order deterministic.
		syms := make([]int, 0, len(t.counts))
		for s := range t.counts {
			syms = append(syms, int(s))
		}
		sort.Ints(syms)
		for _, s := range syms {
			c := t.counts[byte(s)]
			if c == 0 {
				continue // explicit: no log0
			}
			p := float64(c) / tt
			hc -= p * math.Log2(p)
		}
		h += (float64(t.total) / float64(total)) * hc
	}
	return h
}

// CodeLength returns the smoothed ideal code length in bits of the
// observed sequence under the conditional model (estimate only). Each
// distinct table is counted once, so merged contexts are coded with
// their shared distribution without double counting.
func (cm *CondModel) CodeLength() float64 {
	bits := 0.0
	for _, t := range cm.uniqueTables() {
		denom := float64(t.total) + cm.K*float64(cm.Alpha)
		for _, c := range t.counts {
			if c == 0 {
				continue
			}
			p := (float64(c) + cm.K) / denom
			bits += float64(c) * -math.Log2(p)
		}
	}
	return bits
}
