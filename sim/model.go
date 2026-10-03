package sim

import "math/bits"

// model is the reference implementation the compressed bitmaps are
// checked against during a simulation.
type model interface {
	add(v uint32)
	remove(v uint32)
	addRange(lo, hi uint32) // inclusive
	removeRange(lo, hi uint32)
	contains(v uint32) bool
	count() uint64
	andCount(o model) uint64
	orCount(o model) uint64
	xorCount(o model) uint64
}

// wordModel is a dense bit vector, used for small key spaces.
type wordModel struct {
	words []uint64
	cnt   uint64
}

func newWordModel(keySpace uint32) *wordModel {
	return &wordModel{words: make([]uint64, (uint64(keySpace)+63)/64)}
}

func (m *wordModel) add(v uint32) {
	mask := uint64(1) << (v & 63)
	if m.words[v>>6]&mask == 0 {
		m.words[v>>6] |= mask
		m.cnt++
	}
}

func (m *wordModel) remove(v uint32) {
	mask := uint64(1) << (v & 63)
	if m.words[v>>6]&mask != 0 {
		m.words[v>>6] &^= mask
		m.cnt--
	}
}

func wordRangeMask(lo, hi uint32) uint64 {
	return (^uint64(0) << (lo & 63)) & (^uint64(0) >> (63 - (hi & 63)))
}

func (m *wordModel) addRange(lo, hi uint32) {
	loW, hiW := lo>>6, hi>>6
	if loW == hiW {
		mask := wordRangeMask(lo, hi)
		m.cnt += uint64(bits.OnesCount64(mask &^ m.words[loW]))
		m.words[loW] |= mask
		return
	}
	head := wordRangeMask(lo, 63)
	m.cnt += uint64(bits.OnesCount64(head &^ m.words[loW]))
	m.words[loW] |= head
	for w := loW + 1; w < hiW; w++ {
		m.cnt += uint64(64 - bits.OnesCount64(m.words[w]))
		m.words[w] = ^uint64(0)
	}
	tail := wordRangeMask(0, hi)
	m.cnt += uint64(bits.OnesCount64(tail &^ m.words[hiW]))
	m.words[hiW] |= tail
}

func (m *wordModel) removeRange(lo, hi uint32) {
	loW, hiW := lo>>6, hi>>6
	if loW == hiW {
		mask := wordRangeMask(lo, hi)
		m.cnt -= uint64(bits.OnesCount64(mask & m.words[loW]))
		m.words[loW] &^= mask
		return
	}
	head := wordRangeMask(lo, 63)
	m.cnt -= uint64(bits.OnesCount64(head & m.words[loW]))
	m.words[loW] &^= head
	for w := loW + 1; w < hiW; w++ {
		m.cnt -= uint64(bits.OnesCount64(m.words[w]))
		m.words[w] = 0
	}
	tail := wordRangeMask(0, hi)
	m.cnt -= uint64(bits.OnesCount64(tail & m.words[hiW]))
	m.words[hiW] &^= tail
}

func (m *wordModel) contains(v uint32) bool {
	return m.words[v>>6]&(uint64(1)<<(v&63)) != 0
}

func (m *wordModel) count() uint64 { return m.cnt }

func (m *wordModel) andCount(o model) uint64 {
	ow := o.(*wordModel)
	var n uint64
	for i := range m.words {
		n += uint64(bits.OnesCount64(m.words[i] & ow.words[i]))
	}
	return n
}

func (m *wordModel) orCount(o model) uint64 {
	return m.cnt + o.count() - m.andCount(o)
}

func (m *wordModel) xorCount(o model) uint64 {
	return m.cnt + o.count() - 2*m.andCount(o)
}

// mapModel is a sparse reference model, used for very large key spaces.
type mapModel struct {
	set map[uint32]struct{}
}

func newMapModel() *mapModel {
	return &mapModel{set: make(map[uint32]struct{})}
}

func (m *mapModel) add(v uint32)    { m.set[v] = struct{}{} }
func (m *mapModel) remove(v uint32) { delete(m.set, v) }

func (m *mapModel) addRange(lo, hi uint32) {
	for v := lo; ; v++ {
		m.set[v] = struct{}{}
		if v == hi {
			break
		}
	}
}

func (m *mapModel) removeRange(lo, hi uint32) {
	for v := lo; ; v++ {
		delete(m.set, v)
		if v == hi {
			break
		}
	}
}

func (m *mapModel) contains(v uint32) bool {
	_, ok := m.set[v]
	return ok
}

func (m *mapModel) count() uint64 { return uint64(len(m.set)) }

func (m *mapModel) andCount(o model) uint64 {
	om := o.(*mapModel)
	small, big := m.set, om.set
	if len(big) < len(small) {
		small, big = big, small
	}
	var n uint64
	for v := range small {
		if _, ok := big[v]; ok {
			n++
		}
	}
	return n
}

func (m *mapModel) orCount(o model) uint64 {
	return m.count() + o.count() - m.andCount(o)
}

func (m *mapModel) xorCount(o model) uint64 {
	return m.count() + o.count() - 2*m.andCount(o)
}
