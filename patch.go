// Package binpatch implements a small binary patch format and a streaming,
// atomic applier. A patch is a sequence of operations that reconstruct a
// target file from a baseline file: copy operations read byte ranges from
// the baseline (never from the partially generated output), and insert
// operations embed literal bytes in the patch itself.
package binpatch

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// OpKind identifies the type of a patch operation.
type OpKind byte

const (
	// OpCopy copies Length bytes from the baseline at Offset into the output.
	OpCopy OpKind = 0
	// OpInsert appends the literal Data bytes to the output.
	OpInsert OpKind = 1
)

// magic identifies the patch stream format.
var magic = []byte("BINPATCH01")

// Op is a single patch operation.
type Op struct {
	Kind   OpKind
	Offset uint64 // OpCopy: read offset within the baseline
	Length uint64 // OpCopy: number of bytes to copy; OpInsert: len(Data)
	Data   []byte // OpInsert: literal bytes to append
}

// Copy returns an Op that copies length baseline bytes starting at offset.
func Copy(offset, length uint64) Op {
	return Op{Kind: OpCopy, Offset: offset, Length: length}
}

// Insert returns an Op that appends data verbatim to the output.
func Insert(data []byte) Op {
	return Op{Kind: OpInsert, Length: uint64(len(data)), Data: data}
}

// Patch describes how to transform a baseline file into a target file.
type Patch struct {
	// BaselineHash is the SHA-256 of the baseline the patch was built against.
	BaselineHash [sha256.Size]byte
	// TargetLength is the exact size in bytes of the reconstructed target.
	TargetLength uint64
	// TargetHash is the SHA-256 of the fully reconstructed target.
	TargetHash [sha256.Size]byte
	// Ops reconstruct the target when applied in order.
	Ops []Op
}

// ErrFormat is returned when a patch stream is malformed or truncated.
var ErrFormat = errors.New("binpatch: malformed patch stream")

// Encode writes the patch to w in the binary stream format.
func (p *Patch) Encode(w io.Writer) error {
	if _, err := w.Write(magic); err != nil {
		return err
	}
	if _, err := w.Write(p.BaselineHash[:]); err != nil {
		return err
	}
	var num [8]byte
	binary.BigEndian.PutUint64(num[:], p.TargetLength)
	if _, err := w.Write(num[:]); err != nil {
		return err
	}
	if _, err := w.Write(p.TargetHash[:]); err != nil {
		return err
	}
	binary.BigEndian.PutUint64(num[:], uint64(len(p.Ops)))
	if _, err := w.Write(num[:]); err != nil {
		return err
	}
	for _, op := range p.Ops {
		if err := encodeOp(w, op); err != nil {
			return err
		}
	}
	return nil
}

func encodeOp(w io.Writer, op Op) error {
	if _, err := w.Write([]byte{byte(op.Kind)}); err != nil {
		return err
	}
	var num [8]byte
	switch op.Kind {
	case OpCopy:
		binary.BigEndian.PutUint64(num[:], op.Offset)
		if _, err := w.Write(num[:]); err != nil {
			return err
		}
		binary.BigEndian.PutUint64(num[:], op.Length)
		if _, err := w.Write(num[:]); err != nil {
			return err
		}
	case OpInsert:
		if uint64(len(op.Data)) != op.Length {
			return fmt.Errorf("binpatch: insert op length %d does not match data length %d", op.Length, len(op.Data))
		}
		binary.BigEndian.PutUint64(num[:], op.Length)
		if _, err := w.Write(num[:]); err != nil {
			return err
		}
		if _, err := w.Write(op.Data); err != nil {
			return err
		}
	default:
		return fmt.Errorf("binpatch: unknown op kind %d", op.Kind)
	}
	return nil
}

// Decode reads a patch from r. The stream must contain exactly one patch;
// trailing data is an error.
func Decode(r io.Reader) (*Patch, error) {
	p, err := decodePatch(r)
	if err != nil {
		return nil, err
	}
	// Reject trailing garbage after the final op.
	var extra [1]byte
	if n, err := r.Read(extra[:]); n > 0 || err != io.EOF {
		return nil, ErrFormat
	}
	return p, nil
}

func decodePatch(r io.Reader) (*Patch, error) {
	p := &Patch{}
	hdr := make([]byte, len(magic))
	if _, err := io.ReadFull(r, hdr); err != nil || string(hdr) != string(magic) {
		return nil, ErrFormat
	}
	if _, err := io.ReadFull(r, p.BaselineHash[:]); err != nil {
		return nil, ErrFormat
	}
	var num [8]byte
	if _, err := io.ReadFull(r, num[:]); err != nil {
		return nil, ErrFormat
	}
	p.TargetLength = binary.BigEndian.Uint64(num[:])
	if _, err := io.ReadFull(r, p.TargetHash[:]); err != nil {
		return nil, ErrFormat
	}
	if _, err := io.ReadFull(r, num[:]); err != nil {
		return nil, ErrFormat
	}
	nops := binary.BigEndian.Uint64(num[:])
	if nops > 1<<31 {
		return nil, ErrFormat
	}
	for i := uint64(0); i < nops; i++ {
		op, err := decodeOp(r)
		if err != nil {
			return nil, err
		}
		p.Ops = append(p.Ops, op)
	}
	return p, nil
}

func decodeOp(r io.Reader) (Op, error) {
	var kind [1]byte
	if _, err := io.ReadFull(r, kind[:]); err != nil {
		return Op{}, ErrFormat
	}
	var num [8]byte
	switch OpKind(kind[0]) {
	case OpCopy:
		var op Op
		op.Kind = OpCopy
		if _, err := io.ReadFull(r, num[:]); err != nil {
			return Op{}, ErrFormat
		}
		op.Offset = binary.BigEndian.Uint64(num[:])
		if _, err := io.ReadFull(r, num[:]); err != nil {
			return Op{}, ErrFormat
		}
		op.Length = binary.BigEndian.Uint64(num[:])
		return op, nil
	case OpInsert:
		if _, err := io.ReadFull(r, num[:]); err != nil {
			return Op{}, ErrFormat
		}
		length := binary.BigEndian.Uint64(num[:])
		if length > 1<<31 {
			return Op{}, ErrFormat
		}
		data := make([]byte, length)
		if _, err := io.ReadFull(r, data); err != nil {
			return Op{}, ErrFormat
		}
		return Op{Kind: OpInsert, Length: length, Data: data}, nil
	default:
		return Op{}, ErrFormat
	}
}
