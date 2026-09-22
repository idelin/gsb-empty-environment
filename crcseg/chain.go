package crcseg

import "fmt"

// Chain 是一串按顺序排列的分段摘要，并始终维护它们合成后的整体摘要。
// 某个子段被替换时，只需重算该段并重做摘要合成，不重新扫描其它段
// 对应的原始数据。
type Chain struct {
	p     Params
	parts []Segment
	total Segment
}

// NewChain 创建空链。
func NewChain(p Params) (*Chain, error) {
	empty, err := EmptySegment(p)
	if err != nil {
		return nil, err
	}
	return &Chain{p: p, total: empty}, nil
}

// Params 返回链使用的 CRC 参数。
func (c *Chain) Params() Params { return c.p }

// Len 返回已包含的分段个数。
func (c *Chain) Len() int { return len(c.parts) }

// SegmentAt 返回第 i 段的摘要。
func (c *Chain) SegmentAt(i int) (Segment, error) {
	if i < 0 || i >= len(c.parts) {
		return Segment{}, fmt.Errorf("%w: %d not in [0,%d)",
			ErrIndexOutOfRange, i, len(c.parts))
	}
	return c.parts[i], nil
}

// Append 在链尾追加一段并更新整体摘要。
func (c *Chain) Append(s Segment) error {
	if err := s.validate(c.p); err != nil {
		return err
	}
	combined, err := Combine(c.p, c.total, s)
	if err != nil {
		return err
	}
	c.parts = append(c.parts, s)
	c.total = combined
	return nil
}

// ReplaceAt 用 seg 替换第 i 段。要求新段与旧段长度完全相同：
// 即使两段摘要恰好相等，长度不同也不允许混用。整体摘要由各段摘要
// 重新合成，过程不读取任何段的原始字节。
func (c *Chain) ReplaceAt(i int, seg Segment) error {
	if i < 0 || i >= len(c.parts) {
		return fmt.Errorf("%w: %d not in [0,%d)",
			ErrIndexOutOfRange, i, len(c.parts))
	}
	if err := seg.validate(c.p); err != nil {
		return err
	}
	old := c.parts[i]
	if seg.Length != old.Length {
		return fmt.Errorf("%w: segment %d old=%d new=%d",
			ErrLengthMismatch, i, old.Length, seg.Length)
	}
	parts := make([]Segment, len(c.parts))
	copy(parts, c.parts)
	parts[i] = seg

	total, err := EmptySegment(c.p)
	if err != nil {
		return err
	}
	for _, s := range parts {
		total, err = Combine(c.p, total, s)
		if err != nil {
			return err
		}
	}
	c.parts = parts
	c.total = total
	return nil
}

// Segment 返回整链对应的整体摘要。
func (c *Chain) Segment() Segment { return c.total }

// Verify 报告整链合成结果是否与 expected 完全一致（长度与摘要都比对）。
// 这是“子段替换后的整体验证”：只做摘要合成，不扫描任何原始数据。
func (c *Chain) Verify(expected Segment) bool {
	return c.total.Length == expected.Length && c.total.CRC == expected.CRC
}
