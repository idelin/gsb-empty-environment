package set32

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// 编码布局（全部小端）：
//
//	u32 magic | u32 blockCount
//	每块：u16 key | u8 kind | u8 reserved(0) | u32 count | payload
//	array  payload: count × u16（严格递增）
//	bitmap payload: 1024 × u64（8192 字节）
//	runs   payload: u32 runCount，之后 runCount × (u16 lo, u16 hi)
//
// 编码是规范的：块按 key 严格递增、无空块、runs 互不相邻、
// 容器类型等于 desiredKind(count, runCount)。同一逻辑集合的编码唯一。
const magic uint32 = 0x52534E31 // "RSN1"

const blockHeaderBytes = 8

// Encode 返回集合的规范编码。
func (s *Set) Encode() []byte {
	out := make([]byte, 0, s.EncodedSize())
	var tmp [8]byte
	binary.LittleEndian.PutUint32(tmp[:4], magic)
	out = append(out, tmp[:4]...)
	binary.LittleEndian.PutUint32(tmp[:4], uint32(len(s.blocks)))
	out = append(out, tmp[:4]...)
	for _, b := range s.blocks {
		binary.LittleEndian.PutUint16(tmp[:2], b.key)
		out = append(out, tmp[:2]...)
		out = append(out, byte(b.c.containerKind()), 0)
		binary.LittleEndian.PutUint32(tmp[:4], uint32(b.n))
		out = append(out, tmp[:4]...)
		switch c := b.c.(type) {
		case *arrayC:
			for _, v := range c.data {
				binary.LittleEndian.PutUint16(tmp[:2], v)
				out = append(out, tmp[:2]...)
			}
		case *bitmapC:
			for _, w := range c.words {
				binary.LittleEndian.PutUint64(tmp[:8], w)
				out = append(out, tmp[:8]...)
			}
		case *runsC:
			binary.LittleEndian.PutUint32(tmp[:4], uint32(len(c.runs)))
			out = append(out, tmp[:4]...)
			for _, r := range c.runs {
				binary.LittleEndian.PutUint16(tmp[:2], r.lo)
				out = append(out, tmp[:2]...)
				binary.LittleEndian.PutUint16(tmp[:2], r.hi)
				out = append(out, tmp[:2]...)
			}
		}
	}
	return out
}

// EncodedSize 返回规范编码的总字节数。
func (s *Set) EncodedSize() int {
	n := 8
	for _, b := range s.blocks {
		n += blockHeaderBytes
		switch b.c.containerKind() {
		case kindArray:
			n += arraySize(b.n)
		case kindBitmap:
			n += bitmapBytes
		default:
			n += 4 + runsSize(b.rc)
		}
	}
	return n
}

// Decode 解析并严格校验规范编码。任何计数不符、未排序、run 重叠/相邻、
// 块 key 乱序、空块、保留字节非零、容器类型非最优或尾部多余字节都会报错。
func Decode(data []byte) (*Set, error) {
	d := &decoder{data: data}
	if d.u32() != magic {
		return nil, errors.New("set32: bad magic")
	}
	nBlocks := d.u32()
	if d.err != nil {
		return nil, d.err
	}
	if uint64(nBlocks)*blockHeaderBytes > uint64(len(data)) {
		return nil, errors.New("set32: block count exceeds input size")
	}
	s := &Set{}
	prevKey := -1
	for bi := 0; bi < int(nBlocks); bi++ {
		key := d.u16()
		kd := kind(d.u8())
		resv := d.u8()
		count := d.u32()
		if d.err != nil {
			return nil, d.err
		}
		if resv != 0 {
			return nil, fmt.Errorf("set32: block %d: reserved byte must be 0", bi)
		}
		if int(key) <= prevKey {
			return nil, fmt.Errorf("set32: block %d: keys not strictly increasing", bi)
		}
		prevKey = int(key)
		if count == 0 {
			return nil, fmt.Errorf("set32: block %d: empty block not allowed", bi)
		}
		if count > 65536 {
			return nil, fmt.Errorf("set32: block %d: count %d exceeds 65536", bi, count)
		}
		b := &block{key: key, n: int(count)}
		switch kd {
		case kindArray:
			vals := make([]uint16, count)
			prev := -1
			for i := range vals {
				v := d.u16()
				if d.err != nil {
					return nil, d.err
				}
				if int(v) <= prev {
					return nil, fmt.Errorf("set32: block %d: array not strictly increasing", bi)
				}
				prev = int(v)
				vals[i] = v
			}
			b.c = &arrayC{data: vals}
			b.rc = len(runsFromArrayCount(vals))
		case kindBitmap:
			var w [bitmapBytes / 8]uint64
			n := 0
			for i := range w {
				w[i] = d.u64()
				n += popcount64(w[i])
			}
			if d.err != nil {
				return nil, d.err
			}
			if n != int(count) {
				return nil, fmt.Errorf("set32: block %d: bitmap popcount %d != count %d", bi, n, count)
			}
			b.c = &bitmapC{words: w, n: n}
			b.rc = len((&bitmapC{words: w, n: n}).toRuns())
		case kindRuns:
			rc := d.u32()
			if d.err != nil {
				return nil, d.err
			}
			if rc == 0 {
				return nil, fmt.Errorf("set32: block %d: runs container with 0 runs", bi)
			}
			if uint64(rc) > 32768 {
				return nil, fmt.Errorf("set32: block %d: run count %d exceeds max 32768", bi, rc)
			}
			rs := make([]run, rc)
			n := 0
			prevHi := -2 // 首个 run 允许 lo == 0
			for i := range rs {
				lo, hi := d.u16(), d.u16()
				if d.err != nil {
					return nil, d.err
				}
				if lo > hi {
					return nil, fmt.Errorf("set32: block %d: run %d has lo > hi", bi, i)
				}
				if int(lo) <= prevHi+1 {
					return nil, fmt.Errorf("set32: block %d: runs overlap or adjacent at run %d", bi, i)
				}
				prevHi = int(hi)
				rs[i] = run{lo, hi}
				n += int(hi) - int(lo) + 1
			}
			if n != int(count) {
				return nil, fmt.Errorf("set32: block %d: runs cover %d values != count %d", bi, n, count)
			}
			b.c = &runsC{runs: rs}
			b.rc = int(rc)
		default:
			return nil, fmt.Errorf("set32: block %d: unknown container kind %d", bi, kd)
		}
		// 规范性：容器类型必须是按实际字节数最小 + 固定优先级选出的那个。
		if want := desiredKind(b.n, b.rc); want != kd {
			return nil, fmt.Errorf("set32: block %d: non-canonical container kind %d, want %d", bi, kd, want)
		}
		s.blocks = append(s.blocks, b)
	}
	if d.off != len(data) {
		return nil, fmt.Errorf("set32: %d trailing bytes", len(data)-d.off)
	}
	return s, nil
}

// runsFromArrayCount 返回排序数组的 runs 表示（仅用于解码时统计 run 数）。
func runsFromArrayCount(vals []uint16) []run {
	return (&arrayC{data: vals}).toRuns()
}

type decoder struct {
	data []byte
	off  int
	err  error
}

func (d *decoder) take(n int) []byte {
	if d.err != nil {
		return nil
	}
	if len(d.data)-d.off < n {
		d.err = errors.New("set32: unexpected end of input")
		return nil
	}
	b := d.data[d.off : d.off+n]
	d.off += n
	return b
}

func (d *decoder) u8() uint8 {
	b := d.take(1)
	if b == nil {
		return 0
	}
	return b[0]
}

func (d *decoder) u16() uint16 {
	b := d.take(2)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint16(b)
}

func (d *decoder) u32() uint32 {
	b := d.take(4)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(b)
}

func (d *decoder) u64() uint64 {
	b := d.take(8)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint64(b)
}
