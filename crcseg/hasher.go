package crcseg

// Hasher 是基于给定参数的流式 CRC-32 计算器，实现标准库 hash.Hash32。
// 字节输入可以通过任意次、任意大小的 Write 分块喂入，结果与一次性
// 计算完全一致。
type Hasher struct {
	p    Params
	raw  uint32 // 裸寄存器状态（不含末异或）
	size int64
}

// NewHasher 创建一个处于初始状态的 Hasher。
func NewHasher(p Params) (*Hasher, error) {
	if !p.Valid() {
		return nil, ErrInvalidParams
	}
	return &Hasher{p: p, raw: p.Init}, nil
}

// Params 返回该 Hasher 使用的 CRC 参数。
func (h *Hasher) Params() Params { return h.p }

// Write 吸收一块字节，永远不返回错误。允许任意分块，包括空块。
func (h *Hasher) Write(p []byte) (int, error) {
	h.raw = update(h.p, h.raw, p)
	h.size += int64(len(p))
	return len(p), nil
}

// Sum32 返回当前已写入数据的最终校验值。
func (h *Hasher) Sum32() uint32 { return h.raw ^ h.p.FinalXOR }

// Size 返回校验值字节数，恒为 4。
func (h *Hasher) Size() int { return 4 }

// BlockSize 返回块大小（1，对流式分块无特殊要求）。
func (h *Hasher) BlockSize() int { return 1 }

// Sum 把当前校验值的 4 字节大端表示追加到 b 后返回，不改变内部状态。
func (h *Hasher) Sum(b []byte) []byte {
	s := h.Sum32()
	return append(b, byte(s>>24), byte(s>>16), byte(s>>8), byte(s))
}

// Reset 回到初始状态。
func (h *Hasher) Reset() {
	h.raw = h.p.Init
	h.size = 0
}

// Segment 返回当前状态对应的分段摘要（可用于后续 Combine/Chain）。
func (h *Hasher) Segment() Segment {
	return Segment{Length: h.size, CRC: h.Sum32()}
}
