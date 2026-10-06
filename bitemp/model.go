package bitemp

// Model is the reference implementation: it keeps every Record as a
// rectangle (valid time x know time) and answers point queries by
// brute-force scan. It exists to cross-check Store.
type Model struct {
	seq  uint64
	recs []Record
}

func NewModel() *Model { return &Model{} }

func (m *Model) Put(key, value string, from, to int64) uint64 {
	return m.add(Record{Key: key, Value: value, From: from, To: to})
}

func (m *Model) Delete(key string, from, to int64) uint64 {
	return m.add(Record{Key: key, From: from, To: to, Del: true})
}

func (m *Model) add(r Record) uint64 {
	if r.From >= r.To {
		return m.seq
	}
	m.seq++
	r.Seq = m.seq
	m.recs = append(m.recs, r)
	return m.seq
}

func (m *Model) Seq() uint64 { return m.seq }

// Get: among rectangles covering the point (t, asOf), greatest Seq wins.
func (m *Model) Get(key string, t int64, asOf uint64) (string, bool) {
	best := -1
	for i := range m.recs {
		r := &m.recs[i]
		if r.Key != key || r.Seq > asOf || t < r.From || t >= r.To {
			continue
		}
		if best < 0 || r.Seq > m.recs[best].Seq {
			best = i
		}
	}
	if best < 0 || m.recs[best].Del {
		return "", false
	}
	return m.recs[best].Value, true
}
