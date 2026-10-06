// Package binpatch implements a small binary patch format and a streaming,
// atomic applier.
//
// A patch is a sequence of operations that transform a baseline byte string
// into a target byte string. Two operation kinds exist:
//
//	Copy   copies a half-open range [Offset, Offset+Length) of the ORIGINAL
//	       baseline into the output. Copies never read from already produced
//	       output, so operation order and offsets always refer to the
//	       baseline.
//	Insert appends literal bytes carried inside the patch itself.
//
// The patch header binds the patch to its inputs and result: it carries the
// SHA-256 of the baseline, the exact target length, and the SHA-256 of the
// target. The applier refuses to publish anything unless all three match.
//
// Wire format (all integers big-endian):
//
//	magic        [8]byte  "BPTCH001"
//	baselineHash [32]byte SHA-256 of the baseline
//	targetLength uint64
//	targetHash   [32]byte SHA-256 of the target
//	opCount      uint64
//	ops          opCount operations:
//	              tag 0x01 (copy):   offset uint64, length uint64
//	              tag 0x02 (insert): length uint64, length raw bytes
package binpatch

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
)

// Magic identifies the patch format and version.
const Magic = "BPTCH001"

// Operation tags.
const (
	tagCopy   byte = 0x01
	tagInsert byte = 0x02
)

// Op is a single patch operation. Exactly one of the operation kinds is
// represented: a copy (Insert == false) reads Length baseline bytes starting
// at Offset; an insert (Insert == true) appends the literal Data bytes.
type Op struct {
	Insert bool
	Offset uint64 // copy only: offset into the original baseline
	Length uint64 // copy only: number of baseline bytes to copy
	Data   []byte // insert only: literal bytes to append
}

// Header is the fixed-size patch preamble.
type Header struct {
	BaselineHash [32]byte
	TargetLength uint64
	TargetHash   [32]byte
	OpCount      uint64
}

// Sentinel errors.
var (
	ErrBadMagic     = errors.New("binpatch: bad magic")
	ErrTruncated    = errors.New("binpatch: truncated patch")
	ErrUnknownOp    = errors.New("binpatch: unknown operation tag")
	ErrExtraData    = errors.New("binpatch: trailing data after last operation")
	ErrBaselineHash = errors.New("binpatch: baseline hash mismatch")
	ErrOutOfBounds  = errors.New("binpatch: copy range outside baseline")
	ErrTargetLength = errors.New("binpatch: target length mismatch")
	ErrTargetHash   = errors.New("binpatch: target hash mismatch")
)

// Encode writes the patch for the given header and operations to w. The
// caller is responsible for ensuring the header matches the baseline/target
// the operations describe; see Diff for a way to build consistent patches.
func Encode(w io.Writer, h Header, ops []Op) error {
	h.OpCount = uint64(len(ops))
	if _, err := io.WriteString(w, Magic); err != nil {
		return err
	}
	if _, err := w.Write(h.BaselineHash[:]); err != nil {
		return err
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], h.TargetLength)
	if _, err := w.Write(buf[:]); err != nil {
		return err
	}
	if _, err := w.Write(h.TargetHash[:]); err != nil {
		return err
	}
	binary.BigEndian.PutUint64(buf[:], h.OpCount)
	if _, err := w.Write(buf[:]); err != nil {
		return err
	}
	for _, op := range ops {
		if op.Insert {
			if _, err := w.Write([]byte{tagInsert}); err != nil {
				return err
			}
			binary.BigEndian.PutUint64(buf[:], uint64(len(op.Data)))
			if _, err := w.Write(buf[:]); err != nil {
				return err
			}
			if _, err := w.Write(op.Data); err != nil {
				return err
			}
			continue
		}
		if _, err := w.Write([]byte{tagCopy}); err != nil {
			return err
		}
		binary.BigEndian.PutUint64(buf[:], op.Offset)
		if _, err := w.Write(buf[:]); err != nil {
			return err
		}
		binary.BigEndian.PutUint64(buf[:], op.Length)
		if _, err := w.Write(buf[:]); err != nil {
			return err
		}
	}
	return nil
}

// Reader decodes a patch stream operation by operation. Operations are
// yielded in order; insert payloads are exposed as an io.Reader so large
// inserts never need to be buffered in memory.
type Reader struct {
	r        io.Reader
	br       io.Reader // buffered view of r for exact reads
	Header   Header
	opsLeft  uint64
	finished bool
}

// NewReader reads the patch header from r and returns a streaming decoder.
func NewReader(r io.Reader) (*Reader, error) {
	pr := &Reader{r: r, br: r}
	magic := make([]byte, len(Magic))
	if _, err := io.ReadFull(pr.br, magic); err != nil {
		return nil, truncErr(err)
	}
	if string(magic) != Magic {
		return nil, ErrBadMagic
	}
	if _, err := io.ReadFull(pr.br, pr.Header.BaselineHash[:]); err != nil {
		return nil, truncErr(err)
	}
	var buf [8]byte
	if _, err := io.ReadFull(pr.br, buf[:]); err != nil {
		return nil, truncErr(err)
	}
	pr.Header.TargetLength = binary.BigEndian.Uint64(buf[:])
	if _, err := io.ReadFull(pr.br, pr.Header.TargetHash[:]); err != nil {
		return nil, truncErr(err)
	}
	if _, err := io.ReadFull(pr.br, buf[:]); err != nil {
		return nil, truncErr(err)
	}
	pr.Header.OpCount = binary.BigEndian.Uint64(buf[:])
	pr.opsLeft = pr.Header.OpCount
	return pr, nil
}

// NextOp advances to the next operation. For inserts it returns a reader
// over exactly the payload bytes; the caller must consume it fully before
// calling NextOp again. It returns io.EOF after the last operation, and
// ErrExtraData if bytes remain in the stream afterwards.
func (pr *Reader) NextOp() (op Op, payload io.Reader, err error) {
	if pr.finished {
		return op, nil, ErrExtraData
	}
	if pr.opsLeft == 0 {
		pr.finished = true
		// A well-formed patch ends exactly after its last operation.
		var one [1]byte
		if _, err := pr.br.Read(one[:]); err == io.EOF {
			return op, nil, io.EOF
		} else if err != nil {
			return op, nil, err
		}
		return op, nil, ErrExtraData
	}
	var tag [1]byte
	if _, err := io.ReadFull(pr.br, tag[:]); err != nil {
		return op, nil, truncErr(err)
	}
	var buf [8]byte
	switch tag[0] {
	case tagCopy:
		if _, err := io.ReadFull(pr.br, buf[:]); err != nil {
			return op, nil, truncErr(err)
		}
		op.Offset = binary.BigEndian.Uint64(buf[:])
		if _, err := io.ReadFull(pr.br, buf[:]); err != nil {
			return op, nil, truncErr(err)
		}
		op.Length = binary.BigEndian.Uint64(buf[:])
	case tagInsert:
		if _, err := io.ReadFull(pr.br, buf[:]); err != nil {
			return op, nil, truncErr(err)
		}
		op.Insert = true
		op.Length = binary.BigEndian.Uint64(buf[:])
		payload = &exactReader{r: pr.br, n: op.Length}
	default:
		return op, nil, fmt.Errorf("%s: 0x%02x", ErrUnknownOp, tag[0])
	}
	pr.opsLeft--
	return op, payload, nil
}

// exactReader yields exactly n bytes from r and reports ErrTruncated if the
// underlying stream ends first.
type exactReader struct {
	r io.Reader
	n uint64
}

func (e *exactReader) Read(p []byte) (int, error) {
	if e.n == 0 {
		return 0, io.EOF
	}
	if uint64(len(p)) > e.n {
		p = p[:e.n]
	}
	n, err := e.r.Read(p)
	e.n -= uint64(n)
	if err == io.EOF && e.n > 0 {
		return n, ErrTruncated
	}
	return n, err
}

// truncErr normalises unexpected EOF while parsing structure into
// ErrTruncated.
func truncErr(err error) error {
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return ErrTruncated
	}
	return err
}

// SumWriter hashes everything written through it.
type SumWriter struct {
	W io.Writer
	H hash.Hash
	N uint64
}

// Write implements io.Writer, updating the running hash and byte count.
func (s *SumWriter) Write(p []byte) (int, error) {
	n, err := s.W.Write(p)
	s.H.Write(p[:n])
	s.N += uint64(n)
	return n, err
}
