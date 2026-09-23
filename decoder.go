package runlzv2

import (
	"encoding/binary"
	"hash/crc32"
	"io"
)

// Reader is a streaming decompressor. A Read that returns (0, nil) marks an
// explicit flush boundary; io.EOF marks the final frame. Small destination
// buffers provide natural backpressure: resuming never repeats bytes.
type Reader struct {
	r        io.Reader
	limit    int64
	produced int64

	frameLeft  uint32
	frameFlags byte
	frameCRC   uint32
	crc        uint32
	inFrame    bool
	sawFrame   bool
	eof        bool
	flushMark  bool

	blockLeft  uint32
	blockKind  byte
	repeatByte byte

	header [FrameHeaderLen]byte
	block  [BlockHeaderLen]byte
}

// NewReader returns a streaming decoder without an output quota.
func NewReader(r io.Reader) *Reader {
	return &Reader{r: r, limit: -1}
}

// NewReaderLimit returns a streaming decoder whose total decompressed output
// is bounded by maxOutput bytes.
func NewReaderLimit(r io.Reader, maxOutput int64) (*Reader, error) {
	if maxOutput < 0 {
		return nil, ErrInvalidArgument
	}
	return &Reader{r: r, limit: maxOutput}, nil
}

func (r *Reader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.flushMark {
		r.flushMark = false
		return 0, nil
	}
	if r.eof {
		return 0, io.EOF
	}

	total := 0
	for total < len(p) {
		if !r.inFrame {
			if err := r.readFrameHeader(); err != nil {
				if err == io.EOF {
					// Clean end only when a final frame was seen or the
					// stream had no frames at all.
					if r.sawFrame {
						return total, ErrFormat
					}
					r.eof = true
					if total > 0 {
						return total, nil
					}
					return 0, io.EOF
				}
				return total, err
			}
		}

		if r.frameLeft == 0 && r.blockLeft == 0 {
			if err := r.finishFrame(); err != nil {
				return total, err
			}
			if r.eof {
				if total > 0 {
					return total, nil
				}
				return 0, io.EOF
			}
			if r.flushMark {
				if total > 0 {
					return total, nil
				}
				r.flushMark = false
				return 0, nil
			}
			continue
		}

		if r.blockLeft == 0 {
			if err := r.readBlockHeader(); err != nil {
				return total, err
			}
		}

		n := int64(r.blockLeft)
		if m := int64(len(p) - total); m < n {
			n = m
		}
		if r.limit >= 0 && r.produced+n > r.limit {
			return total, ErrOutputLimit
		}

		if r.blockKind == tagRepeat {
			for i := int64(0); i < n; i++ {
				p[total] = r.repeatByte
				total++
			}
			r.blockLeft -= uint32(n)
			r.produced += n
		} else {
			got, err := io.ReadFull(r.r, p[total:total+int(n)])
			r.crc = crc32.Update(r.crc, crc32.IEEETable, p[total:total+got])
			total += got
			r.blockLeft -= uint32(got)
			r.frameLeft -= uint32(got)
			r.produced += int64(got)
			if err != nil {
				return total, ErrFormat
			}
		}
	}
	return total, nil
}

func (r *Reader) readFrameHeader() error {
	if _, err := io.ReadFull(r.r, r.header[:]); err != nil {
		if err == io.EOF {
			return io.EOF
		}
		return ErrFormat
	}
	version := r.header[0]
	flags := r.header[1]
	length := binary.BigEndian.Uint32(r.header[2:6])
	if version != Version || !validFlags(flags, int(length)) {
		return ErrFormat
	}
	r.frameFlags = flags
	r.frameLeft = length
	r.frameCRC = binary.BigEndian.Uint32(r.header[6:10])
	r.crc = 0
	r.inFrame = true
	r.sawFrame = true
	return nil
}

func (r *Reader) readBlockHeader() error {
	if r.frameLeft < BlockHeaderLen {
		return ErrFormat
	}
	if _, err := io.ReadFull(r.r, r.block[:]); err != nil {
		return ErrFormat
	}
	r.crc = crc32.Update(r.crc, crc32.IEEETable, r.block[:])
	r.frameLeft -= BlockHeaderLen

	tag := r.block[0]
	length := uint32(binary.BigEndian.Uint16(r.block[1:3])) + 1
	switch tag {
	case tagLiteral:
		if r.frameLeft < length {
			return ErrFormat
		}
		r.blockKind = tagLiteral
		r.blockLeft = length
	case tagRepeat:
		if length < 2 {
			return ErrFormat
		}
		r.blockKind = tagRepeat
		r.blockLeft = length
		r.repeatByte = r.block[3]
	default:
		return ErrFormat
	}
	return nil
}

func (r *Reader) finishFrame() error {
	if r.crc != r.frameCRC {
		return ErrChecksum
	}
	r.inFrame = false
	if r.frameFlags&FlagFinal != 0 {
		r.eof = true
		return nil
	}
	if r.frameFlags&FlagFlush != 0 {
		r.flushMark = true
	}
	return nil
}

// Decode decodes a complete stream (one or more frames) with a bounded
// output quota. Empty input decodes to empty output (no frames).
func Decode(src []byte, maxOutput int64) ([]byte, error) {
	if maxOutput < 0 {
		return nil, ErrInvalidArgument
	}
	r, err := NewReaderLimit(&sliceReader{data: src}, maxOutput)
	if err != nil {
		return nil, err
	}

	var out []byte
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

type sliceReader struct {
	data []byte
	pos  int
}

func (s *sliceReader) Read(p []byte) (int, error) {
	if s.pos >= len(s.data) {
		return 0, io.EOF
	}
	n := copy(p, s.data[s.pos:])
	s.pos += n
	return n, nil
}
