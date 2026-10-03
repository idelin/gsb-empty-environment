package sparse

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"io"
)

// Container encoding tags used by Snapshot.
const (
	tagArray  = 1
	tagBitmap = 2
	tagRun    = 3
)

var (
	errCorrupt   = errors.New("sparse: corrupt snapshot")
	errBadTag    = errors.New("sparse: unknown container tag")
	errTruncated = errors.New("sparse: truncated snapshot")
)

// Snapshot serializes the set into a compact varint/delta-encoded byte
// slice. The encoding is deterministic for a given key set.
func (s *Set) Snapshot() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var buf bytes.Buffer
	tmp := make([]byte, binary.MaxVarintLen64)
	putUvarint := func(v uint64) {
		n := binary.PutUvarint(tmp, v)
		buf.Write(tmp[:n])
	}

	keys := make([]uint32, 0, len(s.cs))
	for hi := range s.cs {
		keys = append(keys, hi)
	}
	for i := 1; i < len(keys); i++ { // insertion sort; shard counts are small
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}

	putUvarint(uint64(len(keys)))
	for _, hi := range keys {
		c := s.cs[hi]
		putUvarint(uint64(hi))
		switch t := c.(type) {
		case *arrayContainer:
			buf.WriteByte(tagArray)
			putUvarint(uint64(len(t.keys)))
			prev := uint16(0)
			for i, k := range t.keys {
				if i == 0 {
					putUvarint(uint64(k))
				} else {
					putUvarint(uint64(k - prev)) // delta encoding
				}
				prev = k
			}
		case *bitmapContainer:
			buf.WriteByte(tagBitmap)
			word := make([]byte, 8)
			for _, w := range t.bits {
				binary.LittleEndian.PutUint64(word, w)
				buf.Write(word)
			}
		case *runContainer:
			buf.WriteByte(tagRun)
			putUvarint(uint64(len(t.runs)))
			for _, r := range t.runs {
				putUvarint(uint64(r.start))
				putUvarint(uint64(r.length))
			}
		}
	}
	return buf.Bytes()
}

// LoadSnapshot rebuilds a set from Snapshot output.
func LoadSnapshot(data []byte) (*Set, error) {
	r := &byteReader{b: data}
	readUvarint := func() (uint64, error) {
		v, err := binary.ReadUvarint(r)
		if err != nil {
			if err == io.EOF {
				return 0, errTruncated
			}
			return 0, err
		}
		return v, nil
	}

	count, err := readUvarint()
	if err != nil {
		return nil, err
	}
	if count > ContainerSize { // shard keys are 16-bit, so at most 65536 shards
		return nil, errCorrupt
	}
	s := &Set{cs: make(map[uint32]container, count)}
	for i := uint64(0); i < count; i++ {
		hi, err := readUvarint()
		if err != nil {
			return nil, err
		}
		if hi >= ContainerSize {
			return nil, errCorrupt
		}
		tag, err := r.ReadByte()
		if err != nil {
			return nil, errTruncated
		}
		var c container
		switch tag {
		case tagArray:
			n, err := readUvarint()
			if err != nil {
				return nil, err
			}
			if n > ContainerSize {
				return nil, errCorrupt
			}
			a := &arrayContainer{keys: make([]uint16, 0, n)}
			prev := uint64(0)
			for j := uint64(0); j < n; j++ {
				d, err := readUvarint()
				if err != nil {
					return nil, err
				}
				if j == 0 {
					prev = d
				} else {
					prev += d
				}
				if prev >= ContainerSize {
					return nil, errCorrupt
				}
				a.keys = append(a.keys, uint16(prev))
			}
			c = a
		case tagBitmap:
			b := &bitmapContainer{}
			word := make([]byte, 8)
			for w := 0; w < len(b.bits); w++ {
				if _, err := io.ReadFull(r, word); err != nil {
					return nil, errTruncated
				}
				b.bits[w] = binary.LittleEndian.Uint64(word)
			}
			b.card = 0
			for _, w := range b.bits {
				for w != 0 {
					b.card += int(w & 1)
					w >>= 1
				}
			}
			c = b
		case tagRun:
			n, err := readUvarint()
			if err != nil {
				return nil, err
			}
			if n > ContainerSize/2+1 {
				return nil, errCorrupt
			}
			rc := &runContainer{runs: make([]interval, 0, n)}
			for j := uint64(0); j < n; j++ {
				start, err := readUvarint()
				if err != nil {
					return nil, err
				}
				length, err := readUvarint()
				if err != nil {
					return nil, err
				}
				if start >= ContainerSize || length >= ContainerSize || start+length >= ContainerSize {
					return nil, errCorrupt
				}
				rc.runs = append(rc.runs, interval{start: uint16(start), length: uint16(length)})
			}
			c = rc
		default:
			return nil, errBadTag
		}
		s.cs[uint32(hi)] = c
		s.card += c.cardinality()
	}
	if r.off != len(r.b) {
		return nil, errCorrupt // trailing garbage
	}
	return s, nil
}

// CompressSnapshot gzip-compresses a snapshot for storage or transfer.
func CompressSnapshot(snap []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write(snap)
	w.Close()
	return buf.Bytes()
}

// DecompressSnapshot reverses CompressSnapshot.
func DecompressSnapshot(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// byteReader adapts a byte slice to io.ByteReader/io.Reader.
type byteReader struct {
	b   []byte
	off int
}

func (r *byteReader) ReadByte() (byte, error) {
	if r.off >= len(r.b) {
		return 0, io.EOF
	}
	b := r.b[r.off]
	r.off++
	return b, nil
}

func (r *byteReader) Read(p []byte) (int, error) {
	if r.off >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.off:])
	r.off += n
	return n, nil
}
