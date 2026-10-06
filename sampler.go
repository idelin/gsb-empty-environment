package wsample

import "errors"

// ErrTooMany is returned by Sample when k exceeds the number of live items.
var ErrTooMany = errors.New("wsample: k exceeds number of items")

// ErrBadWeight is returned when a non-positive weight is supplied.
var ErrBadWeight = errors.New("wsample: weight must be a positive integer")

// Event is a single streaming update. Remove=true revokes the item;
// otherwise the item is inserted or updated with the given Weight.
type Event struct {
	ID     string
	Weight int64
	Remove bool
}

// fenwick is a binary indexed tree over int64 weights, 1-indexed internally.
type fenwick struct {
	tree []int64
}

func newFenwick(n int) *fenwick {
	return &fenwick{tree: make([]int64, n+1)}
}

// add applies delta to position i (0-indexed).
func (f *fenwick) add(i int, delta int64) {
	for j := i + 1; j < len(f.tree); j += j & -j {
		f.tree[j] += delta
	}
}

// sum returns the prefix sum of positions [0, i).
func (f *fenwick) sum(i int) int64 {
	var s int64
	for j := i; j > 0; j -= j & -j {
		s += f.tree[j]
	}
	return s
}

// find returns the 0-indexed position of the slot containing cumulative
// value v, i.e. the smallest i with prefix sum over [0, i] strictly
// greater than v. Requires 0 <= v < total.
func (f *fenwick) find(v int64) int {
	idx := 0
	for bit := highestBit(len(f.tree) - 1); bit > 0; bit >>= 1 {
		next := idx + bit
		if next < len(f.tree) && f.tree[next] <= v {
			idx = next
			v -= f.tree[next]
		}
	}
	return idx // idx is 1-indexed parent; position idx (0-indexed) is the answer
}

func highestBit(n int) int {
	b := 1
	for b<<1 <= n {
		b <<= 1
	}
	return b
}

// Sampler maintains a mutable set of items with positive integer weights
// and draws k distinct items with probability proportional to successive
// remaining weights: at each draw, item i is chosen with probability
// w_i / (sum of weights of items not yet drawn).
type Sampler struct {
	src     Source
	fw      *fenwick
	index   map[string]int // id -> slot
	ids     []string       // slot -> id, "" if slot is free
	weights []int64        // slot -> current weight
	free    []int          // recycled slots
	total   int64
}

// New returns a Sampler drawing randomness from src. If src is nil a
// SplitMix64 seeded with 1 is used.
func New(src Source) *Sampler {
	if src == nil {
		src = NewSplitMix64(1)
	}
	return &Sampler{
		src:   src,
		fw:    newFenwick(8),
		index: make(map[string]int),
	}
}

// Len returns the number of live items.
func (s *Sampler) Len() int {
	return len(s.index)
}

// Total returns the sum of weights of all live items.
func (s *Sampler) Total() int64 {
	return s.total
}

// Weight reports the current weight of id and whether it is present.
func (s *Sampler) Weight(id string) (int64, bool) {
	i, ok := s.index[id]
	if !ok {
		return 0, false
	}
	return s.weights[i], true
}

// Add inserts id with weight w, or updates its weight if already present.
// The weight delta is applied to the index structure, so updates never
// leave residual weight behind.
func (s *Sampler) Add(id string, w int64) error {
	if w <= 0 {
		return ErrBadWeight
	}
	if i, ok := s.index[id]; ok {
		delta := w - s.weights[i]
		s.weights[i] = w
		s.fw.add(i, delta)
		s.total += delta
		return nil
	}
	i := s.allocSlot()
	s.index[id] = i
	s.ids[i] = id
	s.weights[i] = w
	s.fw.add(i, w)
	s.total += w
	return nil
}

// Remove revokes id, completely withdrawing its weight. It reports
// whether the item was present.
func (s *Sampler) Remove(id string) bool {
	i, ok := s.index[id]
	if !ok {
		return false
	}
	s.fw.add(i, -s.weights[i])
	s.total -= s.weights[i]
	s.weights[i] = 0
	s.ids[i] = ""
	delete(s.index, id)
	s.free = append(s.free, i)
	return true
}

// Apply processes one streaming Event.
func (s *Sampler) Apply(ev Event) error {
	if ev.Remove {
		s.Remove(ev.ID)
		return nil
	}
	return s.Add(ev.ID, ev.Weight)
}

// Sample draws k distinct items. The returned slice is in draw order.
// Weights of drawn items are subtracted for subsequent draws and restored
// afterwards, so the Sampler is unchanged by the call.
func (s *Sampler) Sample(k int) ([]string, error) {
	if k < 0 || k > len(s.index) {
		return nil, ErrTooMany
	}
	out := make([]string, 0, k)
	picked := make([]int, 0, k)
	remaining := s.total
	for len(out) < k {
		r := s.src.Int63n(remaining)
		i := s.fw.find(r)
		w := s.weights[i]
		s.fw.add(i, -w)
		remaining -= w
		picked = append(picked, i)
		out = append(out, s.ids[i])
	}
	for _, i := range picked {
		s.fw.add(i, s.weights[i])
	}
	return out, nil
}

func (s *Sampler) allocSlot() int {
	if n := len(s.free); n > 0 {
		i := s.free[n-1]
		s.free = s.free[:n-1]
		return i
	}
	i := len(s.ids)
	if i+1 >= len(s.fw.tree) {
		s.grow()
	}
	s.ids = append(s.ids, "")
	s.weights = append(s.weights, 0)
	return i
}

func (s *Sampler) grow() {
	n := len(s.fw.tree) - 1
	nn := newFenwick(2 * n)
	for i := 0; i < n; i++ {
		w := s.fw.sum(i+1) - s.fw.sum(i)
		if w != 0 {
			nn.add(i, w)
		}
	}
	s.fw = nn
}
