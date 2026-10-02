package roaring

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
)

const magicNumber uint32 = 0x314D4252 // "RBM1"

func writeUint16(buf *bytes.Buffer, v uint16) {
	var tmp [2]byte
	binary.LittleEndian.PutUint16(tmp[:], v)
	buf.Write(tmp[:])
}

func writeUint32(buf *bytes.Buffer, v uint32) {
	var tmp [4]byte
	binary.LittleEndian.PutUint32(tmp[:], v)
	buf.Write(tmp[:])
}

func writeUint64(buf *bytes.Buffer, v uint64) {
	var tmp [8]byte
	binary.LittleEndian.PutUint64(tmp[:], v)
	buf.Write(tmp[:])
}

// Marshal serializes the bitmap into a self-checksummed byte slice.
// Layout: magic | nkeys | [key,type,card,payload]... | crc32(body).
func (b *Bitmap) Marshal() []byte {
	b.mu.RLock()
	defer b.mu.RUnlock()
	buf := bytes.NewBuffer(make([]byte, 0, 256))
	writeUint32(buf, magicNumber)
	writeUint32(buf, uint32(len(b.keys)))
	for i, key := range b.keys {
		c := b.cs[i]
		writeUint64(buf, key)
		buf.WriteByte(c.typeID())
		writeUint32(buf, uint32(c.cardinality()))
		switch t := c.(type) {
		case *arrayContainer:
			for _, v := range t.data {
				writeUint16(buf, v)
			}
		case *bitmapContainer:
			for _, w := range t.bits {
				writeUint64(buf, w)
			}
		case *runContainer:
			writeUint32(buf, uint32(len(t.runs)))
			for _, iv := range t.runs {
				writeUint16(buf, iv.start)
				writeUint16(buf, iv.last)
			}
		}
	}
	out := buf.Bytes()
	crc := crc32.ChecksumIEEE(out)
	return append(out, byte(crc), byte(crc>>8), byte(crc>>16), byte(crc>>24))
}

// reader is a bounds-checked cursor used to defend against corrupt input.
type reader struct {
	data []byte
	off  int
	err  error
}

func (r *reader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || len(r.data)-r.off < n {
		r.err = ErrCorrupt
		return nil
	}
	p := r.data[r.off : r.off+n]
	r.off += n
	return p
}

func (r *reader) u8() uint8 {
	p := r.take(1)
	if p == nil {
		return 0
	}
	return p[0]
}

func (r *reader) u16() uint16 {
	p := r.take(2)
	if p == nil {
		return 0
	}
	return binary.LittleEndian.Uint16(p)
}

func (r *reader) u32() uint32 {
	p := r.take(4)
	if p == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(p)
}

func (r *reader) u64() uint64 {
	p := r.take(8)
	if p == nil {
		return 0
	}
	return binary.LittleEndian.Uint64(p)
}

// Unmarshal parses data produced by Marshal. It validates the magic, the
// trailing CRC32, every length field and all container invariants, so a
// torn write or bit-rot is reported as ErrCorrupt instead of a panic.
func Unmarshal(data []byte) (*Bitmap, error) {
	if len(data) < 12 {
		return nil, ErrCorrupt
	}
	body := data[:len(data)-4]
	want := binary.LittleEndian.Uint32(data[len(data)-4:])
	if crc32.ChecksumIEEE(body) != want {
		return nil, ErrCorrupt
	}
	r := &reader{data: body}
	if r.u32() != magicNumber {
		return nil, ErrCorrupt
	}
	n := r.u32()
	if r.err != nil {
		return nil, r.err
	}
	b := New()
	var lastKey uint64
	for k := uint32(0); k < n; k++ {
		key := r.u64()
		typ := r.u8()
		card := r.u32()
		if r.err != nil {
			return nil, r.err
		}
		if card > 1<<16 {
			return nil, ErrCorrupt
		}
		if k > 0 && key <= lastKey {
			return nil, ErrCorrupt
		}
		lastKey = key
		var c container
		switch typ {
		case typeArray:
			arr := &arrayContainer{data: make([]uint16, card)}
			for i := range arr.data {
				v := r.u16()
				if i > 0 && v <= arr.data[i-1] {
					return nil, ErrCorrupt
				}
				arr.data[i] = v
			}
			c = arr
		case typeBitmap:
			bc := &bitmapContainer{}
			for i := 0; i < bitmapWords; i++ {
				bc.bits[i] = r.u64()
			}
			recomputed := 0
			for _, w := range bc.bits {
				recomputed += bitsOnes(w)
			}
			if recomputed != int(card) {
				return nil, ErrCorrupt
			}
			bc.card = int(card)
			c = bc
		case typeRun:
			nr := r.u32()
			if r.err != nil {
				return nil, r.err
			}
			if nr > 1<<16 {
				return nil, ErrCorrupt
			}
			rc := &runContainer{runs: make([]interval, nr)}
			total := 0
			for i := range rc.runs {
				s, l := r.u16(), r.u16()
				if l < s {
					return nil, ErrCorrupt
				}
				if i > 0 && uint32(s) <= uint32(rc.runs[i-1].last) {
					return nil, ErrCorrupt
				}
				rc.runs[i] = interval{s, l}
				total += int(l) - int(s) + 1
			}
			if total != int(card) {
				return nil, ErrCorrupt
			}
			c = rc
		default:
			return nil, ErrCorrupt
		}
		if r.err != nil {
			return nil, r.err
		}
		b.keys = append(b.keys, key)
		b.cs = append(b.cs, c)
	}
	if r.off != len(body) {
		return nil, ErrCorrupt
	}
	return b, nil
}

func bitsOnes(w uint64) int {
	n := 0
	for w != 0 {
		w &= w - 1
		n++
	}
	return n
}
