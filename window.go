// Package windowfn implements vectorized window-function accumulators:
// sliding SUM / AVG and RANK over partitioned, ordered column batches.
//
// Columns are plain slices (vectors). Rows are first stably sorted by
// (partition key, order key); each maximal run of equal partition keys
// forms a partition. Frame results are written back in original row order.
package windowfn

import "sort"

// FrameMode selects ROWS or RANGE frame semantics.
type FrameMode int

const (
	// Rows counts physical rows: ROWS BETWEEN n PRECEDING AND CURRENT ROW.
	Rows FrameMode = iota
	// Range counts order-key distance: RANGE BETWEEN n PRECEDING AND CURRENT
	// ROW, including all peers (rows sharing the current order key).
	Range
)

// Frame describes a window frame that ends at the current row.
type Frame struct {
	Mode      FrameMode
	Preceding int64 // rows back (Rows) or order-key offset (Range)
	Unbounded bool  // frame starts at the partition beginning
}

// RowsPreceding is shorthand for ROWS BETWEEN n PRECEDING AND CURRENT ROW.
func RowsPreceding(n int64) Frame { return Frame{Mode: Rows, Preceding: n} }

// RangePreceding is shorthand for RANGE BETWEEN n PRECEDING AND CURRENT ROW.
func RangePreceding(n int64) Frame { return Frame{Mode: Range, Preceding: n} }

// UnboundedRows is ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW.
func UnboundedRows() Frame { return Frame{Mode: Rows, Unbounded: true} }

// Partition is a half-open range [Start, End) of positions in the sorted
// permutation, all sharing one partition key.
type Partition struct{ Start, End int }

// SortPerm stably sorts row indices [0,n) by less and returns the
// permutation plus boundaries of maximal runs equal under samePartition.
// less must order by partition key first, then order key.
func SortPerm(n int, less func(i, j int) bool, samePartition func(i, j int) bool) ([]int, []Partition) {
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	sort.SliceStable(perm, func(a, b int) bool { return less(perm[a], perm[b]) })
	var parts []Partition
	for i := 0; i < n; {
		j := i + 1
		for j < n && samePartition(perm[j-1], perm[j]) {
			j++
		}
		parts = append(parts, Partition{Start: i, End: j})
		i = j
	}
	return perm, parts
}

// Window is a prepared batch: rows sorted by (partition, order) key with
// partition boundaries precomputed. Aggregates reuse the preparation.
type Window struct {
	order []float64 // order key per original row
	perm  []int     // sorted row indices
	parts []Partition
}

// NewWindow sorts n rows by (partitionKeys, orderKeys) and computes
// partition boundaries. All slices must have equal length.
func NewWindow(partitionKeys []string, orderKeys []float64) *Window {
	n := len(orderKeys)
	if len(partitionKeys) != n {
		panic("windowfn: partition/order column length mismatch")
	}
	perm, parts := SortPerm(n,
		func(i, j int) bool {
			if partitionKeys[i] != partitionKeys[j] {
				return partitionKeys[i] < partitionKeys[j]
			}
			return orderKeys[i] < orderKeys[j]
		},
		func(i, j int) bool { return partitionKeys[i] == partitionKeys[j] })
	return &Window{order: orderKeys, perm: perm, parts: parts}
}

// Len returns the number of rows in the batch.
func (w *Window) Len() int { return len(w.perm) }

// Sum computes SUM(values) OVER (... frame), returning a result vector in
// original row order.
func (w *Window) Sum(values []float64, f Frame) []float64 {
	sums, _ := w.sliding(values, f)
	return sums
}

// Avg computes AVG(values) OVER (... frame) as sliding sum / frame count.
func (w *Window) Avg(values []float64, f Frame) []float64 {
	sums, counts := w.sliding(values, f)
	out := make([]float64, len(sums))
	for i := range sums {
		out[i] = sums[i] / float64(counts[i])
	}
	return out
}

// sliding is the vectorized rolling accumulator. For each partition it
// walks sorted positions once, adding the incoming row's value and
// subtracting values that slide out of the frame — O(n) per aggregate.
// It returns per-row sums and frame sizes, indexed by original row.
func (w *Window) sliding(values []float64, f Frame) ([]float64, []int) {
	if len(values) != len(w.perm) {
		panic("windowfn: value column length mismatch")
	}
	sums := make([]float64, len(values))
	counts := make([]int, len(values))
	for _, p := range w.parts {
		if f.Mode == Rows {
			w.slideRows(values, p, f, sums, counts)
		} else {
			w.slideRange(values, p, f, sums, counts)
		}
	}
	return sums, counts
}

// slideRows handles ROWS frames: the frame for the row at sorted position
// pos is [max(partStart, pos-Preceding), pos]. lo only moves forward, so
// each value is added once and subtracted at most once.
func (w *Window) slideRows(values []float64, p Partition, f Frame, sums []float64, counts []int) {
	lo := p.Start
	var acc float64
	for pos := p.Start; pos < p.End; pos++ {
		acc += values[w.perm[pos]] // row enters the frame
		if !f.Unbounded {
			for pos-lo > int(f.Preceding) { // rows slid out of the frame
				acc -= values[w.perm[lo]]
				lo++
			}
		}
		row := w.perm[pos]
		sums[row] = acc
		counts[row] = pos - lo + 1
	}
}

// slideRange handles RANGE offset PRECEDING TO CURRENT ROW. SQL semantics
// include every peer of the current row, so rows are processed one peer
// group at a time; hi/lo only move forward across the whole partition.
func (w *Window) slideRange(values []float64, p Partition, f Frame, sums []float64, counts []int) {
	lo, hi := p.Start, p.Start
	var acc float64
	for pos := p.Start; pos < p.End; {
		grp := pos + 1 // peer group: equal order keys
		for grp < p.End && w.order[w.perm[grp]] == w.order[w.perm[pos]] {
			grp++
		}
		cur := w.order[w.perm[pos]]
		for hi < grp { // peer group enters the frame
			acc += values[w.perm[hi]]
			hi++
		}
		if !f.Unbounded {
			limit := cur - float64(f.Preceding)
			for lo < p.End && w.order[w.perm[lo]] < limit { // keys fell out of range
				acc -= values[w.perm[lo]]
				lo++
			}
		}
		for k := pos; k < grp; k++ {
			row := w.perm[k]
			sums[row] = acc
			counts[row] = hi - lo
		}
		pos = grp
	}
}

// Rank computes RANK() OVER (PARTITION BY ... ORDER BY ...): 1-based rank
// within each partition; peers share a rank, followed by gaps.
func (w *Window) Rank() []int64 {
	out := make([]int64, len(w.perm))
	for _, p := range w.parts {
		for pos := p.Start; pos < p.End; {
			grp := pos + 1
			for grp < p.End && w.order[w.perm[grp]] == w.order[w.perm[pos]] {
				grp++
			}
			r := int64(pos - p.Start + 1)
			for k := pos; k < grp; k++ {
				out[w.perm[k]] = r
			}
			pos = grp
		}
	}
	return out
}
