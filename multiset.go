// Package multiset implements an ordered multiset (compressed sorted index).
//
// In-memory layout (three parallel slices, kept in sync):
//
//	values []int64   distinct values, strictly ascending
//	counts []uint64  multiplicity of values[i], always >= 1
//	prefix []uint64  cumulative multiplicity: prefix[i] = counts[0]+...+counts[i]
//
// On-disk / wire encoding (Encode) is a real compressed layout, not a map dump:
//
//	uvarint  n                       number of distinct values
//	n x { varint valueDelta          zigzag(value[i] - value[i-1]); first is zigzag(value[0])
//	      uvarint count }            multiplicity of value[i]
//	n x   uvarint prefixDelta        prefix[i] - prefix[i-1]; first is prefix[0]
//
// SizeBytes() reports the exact number of bytes of this encoding.
package multiset

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

var ErrNegativeCount = errors.New("multiset: batch rejected, a count would become negative")

// Delta is one atomic batch entry: Diff > 0 inserts, Diff < 0 removes.
type Delta struct {
	Value int64
	Diff  int64
}

// Multiset is an ordered multiset backed by sorted value/count/prefix arrays.
type Multiset struct {
	values []int64
	counts []uint64
	prefix []uint64
	total  uint64
}

// FromSlice builds a multiset from an unordered slice (duplicates kept).
func FromSlice(xs []int64) *Multiset {
	sorted := append([]int64(nil), xs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	m := &Multiset{}
	for _, v := range sorted {
		if n := len(m.values); n > 0 && m.values[n-1] == v {
			m.counts[n-1]++
		} else {
			m.values = append(m.values, v)
			m.counts = append(m.counts, 1)
		}
	}
	m.rebuildPrefix()
	return m
}

func (m *Multiset) rebuildPrefix() {
	m.prefix = make([]uint64, len(m.counts))
	m.total = 0
	for i, c := range m.counts {
		m.total += c
		m.prefix[i] = m.total
	}
}

// find returns (index, true) if value exists, else (insertion point, false).
func (m *Multiset) find(value int64) (int, bool) {
	i := sort.Search(len(m.values), func(i int) bool { return m.values[i] >= value })
	if i < len(m.values) && m.values[i] == value {
		return i, true
	}
	return i, false
}

// Len returns the total number of elements counting duplicates.
func (m *Multiset) Len() uint64 { return m.total }

// Distinct returns the number of distinct values.
func (m *Multiset) Distinct() int { return len(m.values) }

// Count returns the multiplicity of a single value.
func (m *Multiset) Count(value int64) uint64 {
	if i, ok := m.find(value); ok {
		return m.counts[i]
	}
	return 0
}

// Rank returns the cumulative multiplicity: number of elements <= value.
func (m *Multiset) Rank(value int64) uint64 {
	i := sort.Search(len(m.values), func(i int) bool { return m.values[i] > value })
	if i == 0 {
		return 0
	}
	return m.prefix[i-1]
}

// Select returns the k-th smallest element (1-based, duplicates counted).
// e.g. for [1,1,3]: Select(2) == 1.
func (m *Multiset) Select(k uint64) (int64, error) {
	if k == 0 || k > m.total {
		return 0, fmt.Errorf("multiset: select %d out of range [1,%d]", k, m.total)
	}
	i := sort.Search(len(m.prefix), func(i int) bool { return m.prefix[i] >= k })
	return m.values[i], nil
}

// Apply atomically applies a batch of deltas. If any value's resulting count
// would drop below zero, the whole batch is rejected and the multiset is
// left untouched (all-or-nothing).
func (m *Multiset) Apply(batch ...Delta) error {
	net := make(map[int64]int64, len(batch))
	for _, d := range batch {
		if d.Diff == 0 {
			continue
		}
		net[d.Value] += d.Diff
	}
	// Phase 1: validate against current counts. Nothing is mutated here.
	for v, d := range net {
		if d < 0 && uint64(-d) > m.Count(v) {
			return fmt.Errorf("%w: value %d has %d, batch removes %d",
				ErrNegativeCount, v, m.Count(v), -d)
		}
	}
	// Phase 2: commit. Validation guarantees no count can go negative.
	keys := make([]int64, 0, len(net))
	for v := range net {
		keys = append(keys, v)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, v := range keys {
		d := net[v]
		i, ok := m.find(v)
		switch {
		case d > 0 && !ok:
			m.values = append(m.values, 0)
			m.counts = append(m.counts, 0)
			copy(m.values[i+1:], m.values[i:])
			copy(m.counts[i+1:], m.counts[i:])
			m.values[i], m.counts[i] = v, uint64(d)
		case d > 0:
			m.counts[i] += uint64(d)
		default: // d < 0, ok must hold (validated above)
			m.counts[i] -= uint64(-d)
			if m.counts[i] == 0 {
				m.values = append(m.values[:i], m.values[i+1:]...)
				m.counts = append(m.counts[:i], m.counts[i+1:]...)
			}
		}
	}
	m.rebuildPrefix()
	return nil
}

// Intersect returns a new multiset keeping the minimum multiplicity of each
// value present in both. Inputs are not modified.
func (m *Multiset) Intersect(other *Multiset) *Multiset {
	out := &Multiset{}
	i, j := 0, 0
	for i < len(m.values) && j < len(other.values) {
		switch {
		case m.values[i] < other.values[j]:
			i++
		case m.values[i] > other.values[j]:
			j++
		default:
			c := m.counts[i]
			if other.counts[j] < c {
				c = other.counts[j]
			}
			out.values = append(out.values, m.values[i])
			out.counts = append(out.counts, c)
			i++
			j++
		}
	}
	out.rebuildPrefix()
	return out
}

// Elements returns all elements in ascending order, duplicates expanded.
func (m *Multiset) Elements() []int64 {
	out := make([]int64, 0, m.total)
	for i, v := range m.values {
		for c := uint64(0); c < m.counts[i]; c++ {
			out = append(out, v)
		}
	}
	return out
}

// Encode serializes to the compressed layout documented at the top.
func (m *Multiset) Encode() []byte {
	buf := make([]byte, 0, m.SizeBytes())
	buf = binary.AppendUvarint(buf, uint64(len(m.values)))
	prev := int64(0)
	for i, v := range m.values {
		buf = binary.AppendVarint(buf, v-prev)
		buf = binary.AppendUvarint(buf, m.counts[i])
		prev = v
	}
	prevP := uint64(0)
	for _, p := range m.prefix {
		buf = binary.AppendUvarint(buf, p-prevP)
		prevP = p
	}
	return buf
}

// SizeBytes returns the exact byte length of Encode() without allocating.
func (m *Multiset) SizeBytes() int {
	size := len(binary.AppendUvarint(nil, uint64(len(m.values))))
	prev := int64(0)
	for i, v := range m.values {
		size += varintLen(v - prev)
		size += uvarintLen(m.counts[i])
		prev = v
	}
	prevP := uint64(0)
	for _, p := range m.prefix {
		size += uvarintLen(p - prevP)
		prevP = p
	}
	return size
}

func uvarintLen(x uint64) int {
	n := 1
	for x >= 0x80 {
		x >>= 7
		n++
	}
	return n
}

func varintLen(x int64) int {
	ux := uint64(x) << 1
	if x < 0 {
		ux = ^ux
	}
	return uvarintLen(ux)
}

// Decode parses bytes produced by Encode and validates consistency.
func Decode(data []byte) (*Multiset, error) {
	m := &Multiset{}
	n, w := binary.Uvarint(data)
	if w <= 0 {
		return nil, errors.New("multiset: bad header")
	}
	data = data[w:]
	prev := int64(0)
	for i := uint64(0); i < n; i++ {
		d, w := binary.Varint(data)
		if w <= 0 {
			return nil, errors.New("multiset: bad value delta")
		}
		data = data[w:]
		c, w := binary.Uvarint(data)
		if w <= 0 || c == 0 {
			return nil, errors.New("multiset: bad count")
		}
		data = data[w:]
		v := prev + d
		if i > 0 && v <= prev {
			return nil, errors.New("multiset: values not ascending")
		}
		m.values = append(m.values, v)
		m.counts = append(m.counts, c)
		prev = v
	}
	m.rebuildPrefix()
	prevP := uint64(0)
	for i := uint64(0); i < n; i++ {
		d, w := binary.Uvarint(data)
		if w <= 0 {
			return nil, errors.New("multiset: bad prefix delta")
		}
		data = data[w:]
		prevP += d
		if prevP != m.prefix[i] {
			return nil, errors.New("multiset: prefix section inconsistent with counts")
		}
	}
	if len(data) != 0 {
		return nil, errors.New("multiset: trailing bytes")
	}
	return m, nil
}
