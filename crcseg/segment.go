package crcseg

import "math"

// Segment 是一段数据的摘要：最终校验值（已含初末异或）与字节长度。
// 长度是合成运算的必要输入——相同摘要、不同长度的段不能混用。
type Segment struct {
	Length int64
	CRC    uint32
}

// EmptySegment 返回空段（合成恒等元）。空段长度为 0，
// 摘要为 Init ^ FinalXOR。
func EmptySegment(p Params) (Segment, error) {
	if !p.Valid() {
		return Segment{}, ErrInvalidParams
	}
	return Segment{Length: 0, CRC: p.Empty()}, nil
}

// validate 检查分段元数据是否自洽。
func (s Segment) validate(p Params) error {
	if s.Length < 0 {
		return ErrInvalidSegment
	}
	if s.Length == 0 && s.CRC != p.Empty() {
		return ErrInvalidSegment
	}
	return nil
}

// Combine 按 B 跟在 A 之后的顺序合成两段摘要，返回 A||B 的摘要。
//
// 空段是恒等元：Combine(empty, B) == B、Combine(A, empty) == A。
// 长度之和溢出 int64 时返回 ErrLengthOverflow。
// 该操作不读取任何原始数据字节。
func Combine(p Params, a, b Segment) (Segment, error) {
	if !p.Valid() {
		return Segment{}, ErrInvalidParams
	}
	if err := a.validate(p); err != nil {
		return Segment{}, err
	}
	if err := b.validate(p); err != nil {
		return Segment{}, err
	}
	if b.Length == 0 {
		return a, nil
	}
	if a.Length == 0 {
		return b, nil
	}
	if a.Length > math.MaxInt64-b.Length {
		return Segment{}, ErrLengthOverflow
	}
	// 去掉末异或得到裸寄存器状态，合成后再加回。
	rawA := a.CRC ^ p.FinalXOR
	rawB := b.CRC ^ p.FinalXOR
	raw := combineRaw(p, rawA, rawB, b.Length)
	return Segment{Length: a.Length + b.Length, CRC: raw ^ p.FinalXOR}, nil
}

// SegmentFromData 直接由一段字节生成摘要。
func SegmentFromData(p Params, data []byte) (Segment, error) {
	crc, err := p.Checksum(data)
	if err != nil {
		return Segment{}, err
	}
	return Segment{Length: int64(len(data)), CRC: crc}, nil
}
