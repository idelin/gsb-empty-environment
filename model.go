package arithmetic

// BinaryModel is an adaptive frequency model for a binary alphabet {0, 1}.
// Counts start at 1 (Laplace smoothing) so both symbols always have a
// non-zero probability, and adapt as symbols are processed.
type BinaryModel struct {
	Counts [2]uint64
}

// NewBinaryModel returns a BinaryModel with uniform initial counts.
func NewBinaryModel() *BinaryModel {
	return &BinaryModel{Counts: [2]uint64{1, 1}}
}

// Total returns the sum of both symbol frequencies.
func (m *BinaryModel) Total() uint64 {
	return m.Counts[0] + m.Counts[1]
}

// CumRange returns the cumulative frequency range of sym:
// symbol 0 occupies [0, count0), symbol 1 occupies [count0, total).
func (m *BinaryModel) CumRange(sym int) (cumLow, cumHigh uint64) {
	if sym == 0 {
		return 0, m.Counts[0]
	}
	return m.Counts[0], m.Total()
}

// Find resolves a cumulative frequency back to its symbol.
func (m *BinaryModel) Find(cum uint64) (sym int, cumLow, cumHigh uint64) {
	if cum < m.Counts[0] {
		return 0, 0, m.Counts[0]
	}
	return 1, m.Counts[0], m.Total()
}

// Update adapts the model after processing sym.
func (m *BinaryModel) Update(sym int) {
	m.Counts[sym]++
}
