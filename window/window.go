package window

import (
	"fmt"
	"math"
)

// Input is a columnar batch sorted by (PartitionKeys, OrderKeys).
// All columns must have equal length. Rows with equal PartitionKeys
// form one partition; within a partition OrderKeys must be ascending.
type Input struct {
	PartitionKeys []string
	OrderKeys     []float64
	Values        []float64
}

func (in *Input) validate() error {
	n := len(in.Values)
	if len(in.PartitionKeys) != n || len(in.OrderKeys) != n {
		return fmt.Errorf("window: column length mismatch (part=%d order=%d values=%d)",
			len(in.PartitionKeys), len(in.OrderKeys), n)
	}
	for i := 1; i < n; i++ {
		if in.PartitionKeys[i] == in.PartitionKeys[i-1] &&
			in.OrderKeys[i] < in.OrderKeys[i-1] {
			return fmt.Errorf("window: ORDER BY keys not ascending at row %d", i)
		}
	}
	return nil
}

// partitions returns the [start, end) row ranges of each partition.
// It runs in O(n) by scanning the sorted partition-key column.
func partitions(keys []string) [][2]int {
	var out [][2]int
	for i := 0; i < len(keys); {
		j := i + 1
		for j < len(keys) && keys[j] == keys[i] {
			j++
		}
		out = append(out, [2]int{i, j})
		i = j
	}
	return out
}

// frameWindow accumulates sums and counts for a frame over one
// partition [ps, pe). It fills sums/counts for rows ps..pe-1.
//
// The window bounds move monotonically as the current row advances,
// so a running sum is maintained: values entering the window are
// added and values sliding out are subtracted, giving O(1)
// amortized work per row instead of re-summing the whole frame.
type frameWindow struct {
	in  *Input
	f   Frame
	lo  int // inclusive window start (row index)
	hi  int // exclusive window end (row index)
	sum float64
}

func newFrameWindow(in *Input, f Frame, ps int) *frameWindow {
	return &frameWindow{in: in, f: f, lo: ps, hi: ps}
}

// rowBound maps a frame bound to a row index relative to row i.
// For CurrentRow/offset bounds the result may fall outside [ps, pe).
func rowBound(b Bound, i int) int {
	switch b.Kind {
	case UnboundedPreceding:
		return math.MinInt
	case Preceding:
		return i - int(b.Offset)
	case CurrentRow:
		return i
	case Following:
		return i + int(b.Offset)
	case UnboundedFollowing:
		return math.MaxInt
	}
	return i
}

// rangeBound maps a frame bound to an ORDER BY key value at row i.
func rangeBound(b Bound, key float64) float64 {
	switch b.Kind {
	case UnboundedPreceding:
		return math.Inf(-1)
	case Preceding:
		return key - b.Offset
	case CurrentRow:
		return key
	case Following:
		return key + b.Offset
	case UnboundedFollowing:
		return math.Inf(1)
	}
	return key
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// advance slides the window to the frame of row i and returns the
// accumulated sum and row count.
func (w *frameWindow) advance(i, ps, pe int) (float64, int) {
	var tlo, thi int
	if w.f.Mode == Rows {
		// Clamp the inclusive end first so +1 cannot overflow on
		// UNBOUNDED FOLLOWING; clamping keeps tlo <= thi so empty
		// frames collapse naturally.
		tlo = clamp(rowBound(w.f.Start, i), ps, pe)
		thi = clamp(rowBound(w.f.End, i), ps-1, pe-1) + 1
	} else {
		keys := w.in.OrderKeys
		sv := rangeBound(w.f.Start, keys[i])
		ev := rangeBound(w.f.End, keys[i])
		// Both bounds are monotonic in i because keys are sorted.
		tlo, thi = w.lo, w.hi
		for tlo < pe && keys[tlo] < sv {
			tlo++
		}
		for thi < pe && keys[thi] <= ev {
			thi++
		}
		if tlo > thi {
			tlo = thi
		}
	}
	vals := w.in.Values
	for w.hi < thi {
		w.sum += vals[w.hi]
		w.hi++
	}
	for w.hi > thi {
		w.hi--
		w.sum -= vals[w.hi]
	}
	for w.lo < tlo {
		w.sum -= vals[w.lo]
		w.lo++
	}
	for w.lo > tlo {
		w.lo--
		w.sum += vals[w.lo]
	}
	return w.sum, w.hi - w.lo
}

// accumulate computes per-row window sums and counts for the batch.
func accumulate(in *Input, f Frame) (sums []float64, counts []int64, err error) {
	if err := in.validate(); err != nil {
		return nil, nil, err
	}
	if err := f.validate(); err != nil {
		return nil, nil, err
	}
	n := len(in.Values)
	sums = make([]float64, n)
	counts = make([]int64, n)
	for _, p := range partitions(in.PartitionKeys) {
		ps, pe := p[0], p[1]
		w := newFrameWindow(in, f, ps)
		for i := ps; i < pe; i++ {
			s, c := w.advance(i, ps, pe)
			sums[i] = s
			counts[i] = int64(c)
		}
	}
	return sums, counts, nil
}

// Sum computes SUM(Values) OVER (PARTITION BY ... ORDER BY ... frame)
// and returns the result column vector.
func Sum(in *Input, f Frame) ([]float64, error) {
	sums, _, err := accumulate(in, f)
	return sums, err
}

// Avg computes AVG(Values) OVER (... frame). Rows whose frame is
// empty yield NaN, matching SQL's NULL-on-empty-set semantics.
func Avg(in *Input, f Frame) ([]float64, error) {
	sums, counts, err := accumulate(in, f)
	if err != nil {
		return nil, err
	}
	out := make([]float64, len(sums))
	for i := range sums {
		if counts[i] == 0 {
			out[i] = math.NaN()
		} else {
			out[i] = sums[i] / float64(counts[i])
		}
	}
	return out, nil
}

// Rank computes RANK() OVER (PARTITION BY ... ORDER BY ...):
// the 1-based position of each row's peer group within its partition.
// Ties share the same rank; the next distinct key skips ahead.
func Rank(partitionKeys []string, orderKeys []float64) ([]int64, error) {
	in := &Input{PartitionKeys: partitionKeys, OrderKeys: orderKeys, Values: make([]float64, len(orderKeys))}
	if err := in.validate(); err != nil {
		return nil, err
	}
	n := len(orderKeys)
	out := make([]int64, n)
	for _, p := range partitions(partitionKeys) {
		ps, pe := p[0], p[1]
		rank := int64(1)
		for i := ps; i < pe; i++ {
			if i > ps && orderKeys[i] != orderKeys[i-1] {
				rank = int64(i-ps) + 1
			}
			out[i] = rank
		}
	}
	return out, nil
}
