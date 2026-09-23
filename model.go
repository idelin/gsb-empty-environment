package arcode

// SymbolEOF 是终止符，编码器在数据末尾写入它，解码器读到它即确认流结束。
const SymbolEOF = 256

const (
	numSymbols   = 257     // 256 个字节符号 + 1 个终止符
	maxTotalFreq = 1 << 14 // 总频率上限，达到后触发缩放
)

// model 是自适应频率表。编码器与解码器各自维护一份，
// 只要双方对每个符号按相同顺序调用 update，两份表始终保持一致。
type model struct {
	freq  [numSymbols]uint32
	total uint32
}

func newModel() *model {
	m := &model{}
	for i := range m.freq {
		m.freq[i] = 1
	}
	m.total = numSymbols
	return m
}

// cumulative 返回符号 s 的累计频率区间 [cumLow, cumHigh)。
func (m *model) cumulative(s int) (cumLow, cumHigh uint32) {
	var cum uint32
	for i := 0; i < s; i++ {
		cum += m.freq[i]
	}
	return cum, cum + m.freq[s]
}

// find 定位累计频率值 v 所属的符号，返回符号及其累计区间。
func (m *model) find(v uint32) (sym int, cumLow, cumHigh uint32) {
	var cum uint32
	for i := 0; i < numSymbols; i++ {
		next := cum + m.freq[i]
		if v < next {
			return i, cum, next
		}
		cum = next
	}
	// 调用方保证 v < total，正常不会到达这里。
	return -1, 0, 0
}

// update 递增符号 s 的频率；总频率达到上限时整体减半缩放。
// 缩放采用 (f+1)/2，任何已知符号（f >= 1）缩放后仍保持正频率。
func (m *model) update(s int) {
	m.freq[s]++
	m.total++
	if m.total >= maxTotalFreq {
		m.total = 0
		for i := range m.freq {
			m.freq[i] = (m.freq[i] + 1) >> 1
			m.total += m.freq[i]
		}
	}
}
