package recstream

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
)

// splitFrame inspects buf for one complete frame.
//
// On success it returns body (the CRC-free payload), crc, and the total
// number of bytes consumed (length varint + frameLen). body aliases buf, so
// callers that keep it must copy.
//
// ErrNeedMore is returned when buf is a valid prefix but too short.
// ErrCorrupt is returned for a bad length varint, an over-quota frame, a
// truncated CRC varint, or a checksum mismatch. The quota check happens
// before any growth/allocation is implied by the caller.
func splitFrame(buf []byte, maxSize int) (body []byte, crc uint32, n int, err error) {
	frameLen, vn := binary.Uvarint(buf)
	if vn <= 0 {
		if vn == 0 {
			// Could still be a valid (long) prefix; reject a prefix
			// longer than the longest varint as corrupt.
			if len(buf) >= binary.MaxVarintLen64 {
				return nil, 0, 0, ErrCorrupt
			}
			return nil, 0, 0, ErrNeedMore
		}
		return nil, 0, 0, ErrCorrupt // value overflows 64 bits
	}
	if frameLen > uint64(maxSize) {
		return nil, 0, 0, ErrRecordTooLarge
	}
	end := vn + int(frameLen)
	if len(buf) < end {
		return nil, 0, 0, ErrNeedMore
	}
	payload := buf[vn:end]
	if len(payload) == 0 {
		// A frame always carries a CRC varint; frameLen==0 is impossible.
		return nil, 0, 0, ErrCorrupt
	}
	// The CRC is the trailing minimal varint (1..10 bytes). Find the body
	// length whose seeded checksum matches. Trying the trailing widths
	// keeps parsing unambiguous even though the body may end in a byte
	// that itself looks like a varint continuation.
	var (
		bodyOut []byte
		wantOut uint32
		matched bool
	)
	maxCN := 10
	if maxCN > len(payload) {
		maxCN = len(payload)
	}
	for cn := 1; cn <= maxCN; cn++ {
		cand := payload[len(payload)-cn:]
		v, m := binary.Uvarint(cand)
		if m != cn {
			continue // not a complete/minimal varint using exactly cn bytes
		}
		// Canonical encoding: the leading (last-in-stream) byte must use
		// the minimal representation, i.e. when cn>1 the first byte of the
		// crc varint must set the continuation bit (guaranteed by m==cn)
		// and the final byte must be < 0x80 (also guaranteed by Uvarint
		// returning exactly cn).
		b := payload[:len(payload)-cn]
		var len8 [8]byte
		binary.LittleEndian.PutUint64(len8[:], uint64(len(b)))
		h := crc32.New(crcTable)
		h.Write(len8[:])
		h.Write(b)
		if h.Sum32() == uint32(v) {
			bodyOut, wantOut, matched = b, uint32(v), true
			break
		}
	}
	if !matched {
		return nil, 0, 0, ErrCorrupt
	}
	return bodyOut, wantOut, end, nil
}

// parseBody decodes the self-describing fields of a frame body. Returned
// byte slices are copied out of src so they never alias a refill buffer.
// Unknown field IDs are retained; unknown wire types are corrupt (there is
// no safe length with which to skip them).
func parseBody(src []byte) ([]Field, error) {
	var fields []Field
	for len(src) > 0 {
		tag, tn := binary.Uvarint(src)
		if tn <= 0 {
			return nil, ErrCorrupt
		}
		src = src[tn:]
		id, wire := tag>>3, int(tag&7)
		if id == 0 {
			return nil, ErrCorrupt
		}
		f := Field{ID: id, Wire: wire}
		switch wire {
		case WireVarint:
			v, vn := binary.Uvarint(src)
			if vn <= 0 {
				return nil, ErrCorrupt
			}
			src = src[vn:]
			f.Value = v
		case WireFixed32:
			if len(src) < 4 {
				return nil, ErrCorrupt
			}
			f.Value = uint64(binary.LittleEndian.Uint32(src))
			src = src[4:]
		case WireFixed64:
			if len(src) < 8 {
				return nil, ErrCorrupt
			}
			f.Value = binary.LittleEndian.Uint64(src)
			src = src[8:]
		case WireBytes:
			l, ln := binary.Uvarint(src)
			if ln <= 0 || l > uint64(len(src)-ln) {
				return nil, ErrCorrupt
			}
			cp := make([]byte, l)
			copy(cp, src[ln:ln+int(l)])
			f.Raw = cp
			src = src[ln+int(l):]
		default:
			return nil, ErrCorrupt
		}
		fields = append(fields, f)
	}
	return fields, nil
}

// scanner reads framed records from an underlying byte stream with bounded
// buffering. It is shared by the sequential Decoder and the index builder.
type scanner struct {
	r       io.Reader
	maxSize int
	buf     []byte // buffered, unconsumed bytes
	start   int    // start of unconsumed data within buf
	offset  int64  // stream byte offset of buf[start]
}

func newScanner(r io.Reader, maxSize int) *scanner {
	if maxSize < 1 {
		maxSize = 1
	}
	return &scanner{r: r, maxSize: maxSize}
}

// compact moves unconsumed bytes to the front of the buffer.
func (s *scanner) compact() {
	if s.start > 0 {
		n := copy(s.buf, s.buf[s.start:])
		s.buf = s.buf[:n]
		s.start = 0
	}
}

// next returns the next frame body, its crc, its stream offset and its total
// frame size. io.EOF marks a clean end of stream.
func (s *scanner) next() (body []byte, crc uint32, offset, frameSize int64, err error) {
	for {
		avail := s.buf[s.start:]
		if b, c, n, e := splitFrame(avail, s.maxSize); e == nil {
			off := s.offset
			s.start += n
			s.offset += int64(n)
			return b, c, off, int64(n), nil
		} else if !errors.Is(e, ErrNeedMore) {
			return nil, 0, 0, 0, e
		}

		// Need more bytes. First, learn the announced length (if the
		// varint is complete) so growth is bounded by the quota.
		frameLen, vn := binary.Uvarint(avail)
		if vn > 0 && frameLen > uint64(s.maxSize) {
			return nil, 0, 0, 0, ErrRecordTooLarge
		}

		s.compact()
		// Grow, bounded by the announced quota.
		needCap := len(s.buf) + 512
		if vn > 0 {
			want := int(frameLen) + vn
			if want > needCap {
				needCap = want
			}
		}
		if needCap > s.maxSize+binary.MaxVarintLen64 {
			needCap = s.maxSize + binary.MaxVarintLen64
		}
		if cap(s.buf) < needCap {
			nb := make([]byte, len(s.buf), needCap)
			copy(nb, s.buf)
			s.buf = nb
		}
		m, rerr := s.r.Read(s.buf[len(s.buf):cap(s.buf)])
		s.buf = s.buf[:len(s.buf)+m]
		if rerr != nil {
			if rerr == io.EOF {
				if len(s.buf) == 0 {
					return nil, 0, 0, 0, io.EOF
				}
				// Some bytes remain with no chance of more arriving.
				if _, _, _, e := splitFrame(s.buf, s.maxSize); errors.Is(e, ErrNeedMore) {
					return nil, 0, 0, 0, ErrTruncated
				} else if e != nil {
					return nil, 0, 0, 0, e
				}
				continue
			}
			return nil, 0, 0, 0, rerr
		}
	}
}

// Decoder reads records sequentially from an io.Reader.
type Decoder struct {
	s *scanner
}

// DecoderOption configures a Decoder.
type DecoderOption func(*scanner)

// WithDecoderMaxRecordSize sets the length quota for a Decoder.
func WithDecoderMaxRecordSize(n int) DecoderOption {
	return func(s *scanner) { s.maxSize = n }
}

// NewDecoder constructs a sequential decoder.
func NewDecoder(r io.Reader, opts ...DecoderOption) *Decoder {
	s := newScanner(r, DefaultMaxRecordSize)
	for _, o := range opts {
		o(s)
	}
	if s.maxSize < 1 {
		s.maxSize = 1
	}
	return &Decoder{s: s}
}

// Next returns the next record. io.EOF is a clean end; ErrTruncated is an
// announced frame cut short; ErrCorrupt/ErrRecordTooLarge are bad data and
// terminate the stream (no resync by "trying the next byte").
func (d *Decoder) Next() (*Record, error) {
	body, _, _, _, err := d.s.next()
	if err != nil {
		return nil, err
	}
	fields, err := parseBody(body)
	if err != nil {
		return nil, err
	}
	return &Record{Fields: fields}, nil
}

// ChunkDecoder incrementally decodes a byte stream that may arrive in
// arbitrary chunks (including 1-byte chunks). Feed never blocks waiting for
// data: a frame that is not yet complete yields ErrNeedMore and the pending
// bytes are retained for the next Feed.
type ChunkDecoder struct {
	maxSize int
	buf     []byte
	fatal   bool
}

// ChunkOption configures a ChunkDecoder.
type ChunkOption func(*ChunkDecoder)

// WithChunkMaxRecordSize sets the length quota for a ChunkDecoder.
func WithChunkMaxRecordSize(n int) ChunkOption {
	return func(c *ChunkDecoder) { c.maxSize = n }
}

// NewChunkDecoder constructs an incremental decoder.
func NewChunkDecoder(opts ...ChunkOption) *ChunkDecoder {
	c := &ChunkDecoder{maxSize: DefaultMaxRecordSize}
	for _, o := range opts {
		o(c)
	}
	if c.maxSize < 1 {
		c.maxSize = 1
	}
	return c
}

// Feed appends one chunk and returns every complete record it completes, in
// order. ErrNeedMore (with a nil/n empty list) means valid prefix, feed more.
// A bad frame yields ErrCorrupt/ErrRecordTooLarge and latches the decoder:
// later Feeds keep returning the same error, so a corrupt length can never
// silently realign onto following records.
func (c *ChunkDecoder) Feed(chunk []byte) ([]*Record, error) {
	if c.fatal {
		return nil, ErrCorrupt
	}
	c.buf = append(c.buf, chunk...)
	var out []*Record
	for len(c.buf) > 0 {
		body, _, n, err := splitFrame(c.buf, c.maxSize)
		if errors.Is(err, ErrNeedMore) {
			break
		}
		if err != nil {
			c.fatal = true
			if errors.Is(err, ErrRecordTooLarge) {
				return out, err
			}
			return out, err
		}
		fields, perr := parseBody(body)
		if perr != nil {
			c.fatal = true
			return out, perr
		}
		out = append(out, &Record{Fields: fields})
		c.buf = c.buf[n:]
	}
	// Release fully consumed capacity while keeping pending prefixes.
	if len(c.buf) == 0 {
		c.buf = nil
	} else if cap(c.buf) > c.maxSize {
		cp := make([]byte, len(c.buf))
		copy(cp, c.buf)
		c.buf = cp
	}
	if len(out) == 0 && !c.fatal {
		return nil, ErrNeedMore
	}
	return out, nil
}

// Finalize reports whether all fed bytes were consumed. It returns
// ErrTruncated if a partial frame remains and nil on a clean boundary.
func (c *ChunkDecoder) Finalize() error {
	if c.fatal {
		return ErrCorrupt
	}
	if len(c.buf) > 0 {
		return ErrTruncated
	}
	return nil
}

// DecodeAll is a convenience that decodes every record from r.
func DecodeAll(r io.Reader, opts ...DecoderOption) ([]*Record, error) {
	d := NewDecoder(r, opts...)
	var recs []*Record
	for {
		rec, err := d.Next()
		if err == io.EOF {
			return recs, nil
		}
		if err != nil {
			return recs, err
		}
		recs = append(recs, rec)
	}
}

var _ = bytes.MinRead
