package compaction

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// 物理编码使用小端定长头 + u16 长度前缀字节串。页与字典段各有独立
// 魔数，安装器用独立读游标顺序解码，不共享选择器的任何中间结构。
var (
	pageMagic = [4]byte{'C', 'P', 'G', '1'}
	dictMagic = [4]byte{'C', 'D', 'G', '1'}
)

// nilPresent 用于区分“未出现的可选字段”（nil）与“空字节串值”：
// 点值可能合法地为空切片，不能和 nil 混为一谈。
const nilPresent uint8 = 1

// ErrCorruptEncoding 表示物理字节无法按格式解码。
var ErrCorruptEncoding = errors.New("compaction: corrupt encoding")

// enc 是仅用于编码的增量缓冲。
type enc struct {
	b []byte
}

func (e *enc) u8(v uint8)   { e.b = append(e.b, v) }
func (e *enc) u16(v uint16) { e.b = binary.LittleEndian.AppendUint16(e.b, v) }
func (e *enc) u32(v uint32) { e.b = binary.LittleEndian.AppendUint32(e.b, v) }
func (e *enc) u64(v uint64) { e.b = binary.LittleEndian.AppendUint64(e.b, v) }
func (e *enc) bytes(p []byte) {
	if len(p) > 0xffff {
		panic("compaction: encoded field exceeds 64KiB")
	}
	e.u16(uint16(len(p)))
	e.b = append(e.b, p...)
}

// EncodePage 生成候选页的独立编码字节。
func EncodePage(c CandidatePage) []byte {
	e := enc{b: make([]byte, 0, 64)}
	e.b = append(e.b, pageMagic[:]...)
	e.u32(c.ID)
	e.u32(uint32(len(c.Records)))
	for _, r := range c.Records {
		e.u8(uint8(r.Kind))
		e.u64(r.Seq)
		e.bytes(r.Key)
		if r.Kind == KindRangeDelete {
			e.u8(nilPresent)
			e.bytes(r.End)
		} else {
			e.u8(0)
			e.bytes(nil)
		}
		if r.Kind == KindPointValue {
			e.u8(nilPresent)
			e.bytes(r.Value)
		} else {
			e.u8(0)
			e.bytes(nil)
		}
	}
	e.u16(uint16(len(c.DictIDs)))
	for _, d := range c.DictIDs {
		e.u32(d)
	}
	return e.b
}

// EncodeSegment 生成共享字典段的物理编码字节。
func EncodeSegment(d DictSegment) []byte {
	e := enc{b: make([]byte, 0, 16+len(d.Data))}
	e.b = append(e.b, dictMagic[:]...)
	e.u32(d.ID)
	e.u32(uint32(len(d.Data)))
	e.b = append(e.b, d.Data...)
	return e.b
}

// cursor 是安装器自己的顺序读游标，越界即记录错误并停止解码。
type cursor struct {
	b   []byte
	pos int
	err error
}

func newCursor(b []byte) *cursor { return &cursor{b: b} }

func (c *cursor) take(n int) []byte {
	if c.err != nil {
		return nil
	}
	if n < 0 || c.pos+n > len(c.b) {
		c.err = fmt.Errorf("%w: truncated payload", ErrCorruptEncoding)
		return nil
	}
	out := c.b[c.pos : c.pos+n]
	c.pos += n
	return out
}

func (c *cursor) magic(want [4]byte) {
	got := c.take(4)
	if c.err != nil {
		return
	}
	if [4]byte{got[0], got[1], got[2], got[3]} != want {
		c.err = fmt.Errorf("%w: bad magic", ErrCorruptEncoding)
	}
}

func (c *cursor) u8() uint8 {
	p := c.take(1)
	if c.err != nil {
		return 0
	}
	return p[0]
}

func (c *cursor) u16() uint16 {
	p := c.take(2)
	if c.err != nil {
		return 0
	}
	return binary.LittleEndian.Uint16(p)
}

func (c *cursor) u32() uint32 {
	p := c.take(4)
	if c.err != nil {
		return 0
	}
	return binary.LittleEndian.Uint32(p)
}

func (c *cursor) u64() uint64 {
	p := c.take(8)
	if c.err != nil {
		return 0
	}
	return binary.LittleEndian.Uint64(p)
}

// bytes16 读取长度前缀字节串；零长度返回非 nil 空切片，以保留“空值”。
func (c *cursor) bytes16() []byte {
	n := int(c.u16())
	p := c.take(n)
	if c.err != nil {
		return nil
	}
	out := make([]byte, n)
	copy(out, p)
	return out
}

// DecodePage 用独立读游标解码候选页字节。
func DecodePage(b []byte) (CandidatePage, error) {
	c := newCursor(b)
	c.magic(pageMagic)
	out := CandidatePage{}
	out.ID = c.u32()
	n := c.u32()
	if n > 0x100000 {
		return CandidatePage{}, fmt.Errorf("%w: implausible record count", ErrCorruptEncoding)
	}
	out.Records = make([]Record, 0, n)
	for i := uint32(0); i < n; i++ {
		r := Record{}
		r.Kind = Kind(c.u8())
		r.Seq = c.u64()
		r.Key = c.bytes16()
		endPresent := c.u8()
		end := c.bytes16()
		valPresent := c.u8()
		val := c.bytes16()
		switch r.Kind {
		case KindRangeDelete:
			if endPresent != nilPresent {
				return CandidatePage{}, fmt.Errorf("%w: missing range end", ErrCorruptEncoding)
			}
			r.End = end
		case KindPointValue:
			if valPresent != nilPresent {
				return CandidatePage{}, fmt.Errorf("%w: missing point value", ErrCorruptEncoding)
			}
			r.Value = val
		}
		out.Records = append(out.Records, r)
	}
	dn := c.u16()
	out.DictIDs = make([]uint32, 0, dn)
	for i := uint16(0); i < dn; i++ {
		out.DictIDs = append(out.DictIDs, c.u32())
	}
	if c.err != nil {
		return CandidatePage{}, c.err
	}
	if c.pos != len(b) {
		return CandidatePage{}, fmt.Errorf("%w: %d trailing bytes", ErrCorruptEncoding, len(b)-c.pos)
	}
	return out, nil
}

// DecodeSegment 用独立读游标解码字典段字节。
func DecodeSegment(b []byte) (DictSegment, error) {
	c := newCursor(b)
	c.magic(dictMagic)
	d := DictSegment{ID: c.u32()}
	n := c.u32()
	p := c.take(int(n))
	if c.err != nil {
		return DictSegment{}, c.err
	}
	d.Data = make([]byte, n)
	copy(d.Data, p)
	if c.pos != len(b) {
		return DictSegment{}, fmt.Errorf("%w: trailing bytes", ErrCorruptEncoding)
	}
	return d, nil
}
