package roaring

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math/bits"
	"sort"
)

var errCorrupt = errors.New("roaring: corrupt data")

type byteWriter struct{ buf []byte }

func (w *byteWriter) u8(v byte)    { w.buf = append(w.buf, v) }
func (w *byteWriter) u16(v uint16) { w.buf = append(w.buf, byte(v), byte(v>>8)) }
func (w *byteWriter) u32(v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	w.buf = append(w.buf, b[:]...)
}
func (w *byteWriter) u64(v uint64) {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	w.buf = append(w.buf, b[:]...)
}
func (w *byteWriter) bytes(p []byte) { w.buf = append(w.buf, p...) }

type byteReader struct {
	data []byte
	off  int
	err  error
}

func (r *byteReader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || r.off+n > len(r.data) {
		r.err = errCorrupt
		return nil
	}
	p := r.data[r.off : r.off+n]
	r.off += n
	return p
}

func (r *byteReader) u8() byte {
	if p := r.take(1); p != nil {
		return p[0]
	}
	return 0
}

func (r *byteReader) u16() uint16 {
	if p := r.take(2); p != nil {
		return binary.LittleEndian.Uint16(p)
	}
	return 0
}

func (r *byteReader) u32() uint32 {
	if p := r.take(4); p != nil {
		return binary.LittleEndian.Uint32(p)
	}
	return 0
}

func (r *byteReader) u64() uint64 {
	if p := r.take(8); p != nil {
		return binary.LittleEndian.Uint64(p)
	}
	return 0
}

const bitmapMagic = "RBM1"

// Marshal serializes the bitmap deterministically (containers sorted by key).
func (b *Bitmap) Marshal() []byte {
	b.mu.RLock()
	defer b.mu.RUnlock()
	keys := make([]uint16, 0, len(b.containers))
	for k := range b.containers {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	w := &byteWriter{}
	w.bytes([]byte(bitmapMagic))
	w.u32(uint32(len(keys)))
	for _, k := range keys {
		c := b.containers[k]
		w.u16(k)
		w.u8(c.typeID())
		c.writeTo(w)
	}
	return w.buf
}

// Unmarshal replaces the bitmap contents with the decoded data. It returns
// an error on any truncation, corruption, or trailing garbage.
func (b *Bitmap) Unmarshal(data []byte) error {
	r := &byteReader{data: data}
	if magic := r.take(4); r.err != nil || string(magic) != bitmapMagic {
		return errCorrupt
	}
	n := r.u32()
	if r.err != nil {
		return r.err
	}
	containers := make(map[uint16]container, n)
	var prevKey uint16
	for i := uint32(0); i < n; i++ {
		key := r.u16()
		typ := r.u8()
		if r.err != nil {
			return r.err
		}
		if i > 0 && key <= prevKey {
			return errCorrupt
		}
		prevKey = key
		c, err := readContainer(typ, r)
		if err != nil {
			return err
		}
		containers[key] = c
	}
	if r.err != nil {
		return r.err
	}
	if r.off != len(data) {
		return errCorrupt
	}
	b.mu.Lock()
	b.containers = containers
	b.mu.Unlock()
	return nil
}

func readContainer(typ byte, r *byteReader) (container, error) {
	switch typ {
	case 0: // array
		n := r.u32()
		if r.err != nil || n > 1<<16 {
			return nil, errCorrupt
		}
		vals := make([]uint16, n)
		for i := range vals {
			vals[i] = r.u16()
			if i > 0 && vals[i] <= vals[i-1] {
				return nil, errCorrupt
			}
		}
		if r.err != nil {
			return nil, r.err
		}
		return &arrayContainer{vals: vals}, nil
	case 1: // bitmap
		card := r.u32()
		if r.err != nil || card > 1<<16 {
			return nil, errCorrupt
		}
		bc := &bitmapContainer{}
		sum := 0
		for i := range bc.words {
			bc.words[i] = r.u64()
			sum += bits.OnesCount64(bc.words[i])
		}
		if r.err != nil {
			return nil, r.err
		}
		if sum != int(card) {
			return nil, errCorrupt
		}
		bc.card = sum
		return bc, nil
	case 2: // run
		n := r.u32()
		if r.err != nil || n > 1<<15 {
			return nil, errCorrupt
		}
		runs := make([]interval16, n)
		for i := range runs {
			runs[i].start = r.u16()
			runs[i].last = r.u16()
			if runs[i].last < runs[i].start {
				return nil, errCorrupt
			}
			if i > 0 && uint32(runs[i].start) <= uint32(runs[i-1].last)+1 {
				return nil, errCorrupt
			}
		}
		if r.err != nil {
			return nil, r.err
		}
		return &runContainer{runs: runs}, nil
	default:
		return nil, errCorrupt
	}
}

const snapshotMagic = "RSN1"

// marshalSnapshot wraps a serialized bitmap with a magic header and a CRC32
// checksum so that torn or corrupted snapshots are detected on recovery.
func marshalSnapshot(b *Bitmap) []byte {
	payload := b.Marshal()
	w := &byteWriter{}
	w.bytes([]byte(snapshotMagic))
	w.u32(crc32.ChecksumIEEE(payload))
	w.bytes(payload)
	return w.buf
}

func unmarshalSnapshot(data []byte) (*Bitmap, error) {
	r := &byteReader{data: data}
	magic := r.take(4)
	if r.err != nil || string(magic) != snapshotMagic {
		return nil, errCorrupt
	}
	crc := r.u32()
	payload := r.take(len(data) - 8)
	if r.err != nil {
		return nil, r.err
	}
	if crc32.ChecksumIEEE(payload) != crc {
		return nil, errCorrupt
	}
	bm := New()
	if err := bm.Unmarshal(payload); err != nil {
		return nil, err
	}
	return bm, nil
}
