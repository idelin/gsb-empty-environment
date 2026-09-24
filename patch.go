package baselinepatch

import (
	"bytes"
	"errors"
	"fmt"
)

// Operation kinds.
const (
	// OpSet creates or replaces a field (object key or array index).
	// OldExists distinguishes "field existed before" from absence; Old carries
	// its previous value when it did. New is the replacement value (nil means
	// JSON null, not deletion).
	OpSet = 1
	// OpDelete removes an object key. Deleting is not the same as setting null:
	// after deletion key lookup reports absence; Old carries the removed value.
	OpDelete = 2
	// OpSplice edits an array: remove DeleteCount elements at Index, then insert
	// New at the same position. Removed carries the exact deleted elements.
	OpSplice = 3
)

const (
	patchMagic   = "BPDIFF01"
	patchVersion = byte(1)
)

// Sentinel errors. All concrete failures wrap one of these or a decode error.
var (
	// ErrMissingBaseline means Apply was asked to mutate a document but the
	// baseline is absent. It is rejected outright: the implementation never
	// invents an empty object {} or any other default.
	ErrMissingBaseline = errors.New("baselinepatch: missing baseline document")
	// ErrBaselineMismatch means the baseline's digest is not BaselineDigest.
	// In particular, a different document of identical serialized length does
	// not pass: SHA-256 compares content, not length.
	ErrBaselineMismatch = errors.New("baselinepatch: baseline digest mismatch")
	// ErrTargetMismatch means the post-edit digest is not TargetDigest.
	ErrTargetMismatch = errors.New("baselinepatch: target digest mismatch")
	// ErrInvalidPath means a path is empty where a field is required, or
	// traverses a non-container, or uses an invalid array index.
	ErrInvalidPath = errors.New("baselinepatch: invalid path")
	// ErrInvalidOp means an operation is structurally or semantically invalid.
	ErrInvalidOp = errors.New("baselinepatch: invalid operation")
	// ErrInvalidPatch means the serialized patch is malformed.
	ErrInvalidPatch = errors.New("baselinepatch: invalid patch encoding")
)

// Op is one atomic field change or array edit.
type Op struct {
	Kind        int
	Path        []string // object keys; the final element may be an array index
	Index       int      // OpSplice only: start index in the target array
	DeleteCount int      // OpSplice only: number of elements removed
	New         []any    // OpSplice: elements to insert; OpSet: one element
	Removed     []any    // OpSplice: exact elements removed (for inverse/check)

	// OpSet/OpDelete bookkeeping.
	OldExists bool
	Old       any
}

// Patch is an atomic differential against one specific baseline digest.
type Patch struct {
	BaselineDigest [32]byte
	TargetDigest   [32]byte
	Ops            []Op
}

func checkKind(k int) error {
	switch k {
	case OpSet, OpDelete, OpSplice:
		return nil
	default:
		return fmt.Errorf("%w: unknown kind %d", ErrInvalidOp, k)
	}
}

// Validate checks the patch structure independent of any document.
func (p *Patch) Validate() error {
	if p == nil {
		return fmt.Errorf("%w: nil patch", ErrInvalidPatch)
	}
	if len(p.Ops) > maxOps {
		return fmt.Errorf("%w: too many operations", ErrInvalidOp)
	}
	seenSplice := map[string]bool{}
	for i := range p.Ops {
		op := &p.Ops[i]
		if err := checkKind(op.Kind); err != nil {
			return err
		}
		if len(op.Path) == 0 {
			return fmt.Errorf("%w: op %d has empty path", ErrInvalidPath, i)
		}
		if len(op.Path) > maxPathSegments {
			return fmt.Errorf("%w: op %d path too deep", ErrInvalidPath, i)
		}
		for _, seg := range op.Path {
			if seg == "" {
				return fmt.Errorf("%w: op %d has empty path segment", ErrInvalidPath, i)
			}
		}
		switch op.Kind {
		case OpSet:
			if len(op.New) != 1 {
				return fmt.Errorf("%w: set op %d must carry exactly one new value", ErrInvalidOp, i)
			}
			if err := validateValue(op.New[0]); err != nil {
				return err
			}
			if op.OldExists {
				if err := validateValue(op.Old); err != nil {
					return err
				}
			}
			if op.DeleteCount != 0 || len(op.Removed) != 0 || op.Index != 0 {
				return fmt.Errorf("%w: set op %d carries splice fields", ErrInvalidOp, i)
			}
		case OpDelete:
			if len(op.New) != 0 || op.DeleteCount != 0 || op.Index != 0 {
				return fmt.Errorf("%w: delete op %d carries non-delete fields", ErrInvalidOp, i)
			}
			if err := validateValue(op.Old); err != nil {
				return err
			}
		case OpSplice:
			if op.Index < 0 || op.DeleteCount < 0 {
				return fmt.Errorf("%w: splice op %d has negative index/count", ErrInvalidOp, i)
			}
			if len(op.Removed) != op.DeleteCount {
				return fmt.Errorf("%w: splice op %d removed length %d != count %d",
					ErrInvalidOp, i, len(op.Removed), op.DeleteCount)
			}
			for _, v := range op.New {
				if err := validateValue(v); err != nil {
					return err
				}
			}
			for _, v := range op.Removed {
				if err := validateValue(v); err != nil {
					return err
				}
			}
			if op.OldExists {
				return fmt.Errorf("%w: splice op %d carries set fields", ErrInvalidOp, i)
			}
			key := joinPath(op.Path)
			if seenSplice[key] {
				return fmt.Errorf("%w: multiple splices on array %q are not allowed",
					ErrInvalidOp, key)
			}
			seenSplice[key] = true
		}
	}
	return nil
}

// joinPath builds a collision-free key by length-prefixing every segment.
func joinPath(parts []string) string {
	var b bytes.Buffer
	for _, s := range parts {
		writeUvarint(&b, uint64(len(s)))
		b.WriteString(s)
	}
	return b.String()
}

// Marshal serializes the patch in the BPDIFF01 format:
//
//	magic 8 bytes "BPDIFF01"
//	version 1 byte
//	baseline digest 32 bytes
//	target digest 32 bytes
//	uvarint op count
//	per op: kind, uvarint path length, segments, kind-specific payload
func (p *Patch) Marshal() ([]byte, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteString(patchMagic)
	buf.WriteByte(patchVersion)
	buf.Write(p.BaselineDigest[:])
	buf.Write(p.TargetDigest[:])
	writeUvarint(&buf, uint64(len(p.Ops)))
	for i := range p.Ops {
		op := &p.Ops[i]
		buf.WriteByte(byte(op.Kind))
		writeUvarint(&buf, uint64(len(op.Path)))
		for _, seg := range op.Path {
			writeUvarint(&buf, uint64(len(seg)))
			buf.WriteString(seg)
		}
		switch op.Kind {
		case OpSet:
			if op.OldExists {
				buf.WriteByte(1)
			} else {
				buf.WriteByte(0)
			}
			if err := canonicalEncode(&buf, op.New[0]); err != nil {
				return nil, err
			}
			if op.OldExists {
				if err := canonicalEncode(&buf, op.Old); err != nil {
					return nil, err
				}
			}
		case OpDelete:
			if err := canonicalEncode(&buf, op.Old); err != nil {
				return nil, err
			}
		case OpSplice:
			writeUvarint(&buf, uint64(op.Index))
			writeUvarint(&buf, uint64(op.DeleteCount))
			writeUvarint(&buf, uint64(len(op.New)))
			for _, v := range op.New {
				if err := canonicalEncode(&buf, v); err != nil {
					return nil, err
				}
			}
			for _, v := range op.Removed {
				if err := canonicalEncode(&buf, v); err != nil {
					return nil, err
				}
			}
		}
	}
	return buf.Bytes(), nil
}

// Unmarshal decodes a patch. Header and every length prefix are checked
// against the actual buffer before allocation; no host code is executed.
func Unmarshal(data []byte) (*Patch, error) {
	const header = len(patchMagic) + 1 + 32 + 32
	if len(data) < header {
		return nil, fmt.Errorf("%w: short header", ErrInvalidPatch)
	}
	if string(data[:len(patchMagic)]) != patchMagic {
		return nil, fmt.Errorf("%w: bad magic", ErrInvalidPatch)
	}
	if data[len(patchMagic)] != patchVersion {
		return nil, fmt.Errorf("%w: unsupported version", ErrInvalidPatch)
	}
	r := &reader{data: data}
	if _, err := r.bytes(len(patchMagic) + 1); err != nil {
		return nil, err
	}
	p := &Patch{}
	bd, err := r.bytes(32)
	if err != nil {
		return nil, err
	}
	copy(p.BaselineDigest[:], bd)
	td, err := r.bytes(32)
	if err != nil {
		return nil, err
	}
	copy(p.TargetDigest[:], td)

	n64, err := r.readUvarint()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPatch, err)
	}
	if n64 > maxOps {
		return nil, fmt.Errorf("%w: op count exceeds limit", ErrInvalidPatch)
	}
	p.Ops = make([]Op, n64)
	for i := range p.Ops {
		op := &p.Ops[i]
		k, err := r.byte()
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidPatch, err)
		}
		op.Kind = int(k)
		if err := checkKind(op.Kind); err != nil {
			return nil, err
		}
		pc, err := r.readLength(maxPathSegments, "path")
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidPatch, err)
		}
		op.Path = make([]string, pc)
		for j := range op.Path {
			sn, err := r.readLength(maxBinaryBytes, "segment")
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidPatch, err)
			}
			sb, err := r.bytes(sn)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidPatch, err)
			}
			op.Path[j] = string(sb)
		}
		switch op.Kind {
		case OpSet:
			flag, err := r.byte()
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidPatch, err)
			}
			if flag > 1 {
				return nil, fmt.Errorf("%w: bad old-exists flag", ErrInvalidPatch)
			}
			op.OldExists = flag == 1
			nv, err := decodeValue(r)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidPatch, err)
			}
			op.New = []any{nv}
			if op.OldExists {
				ov, err := decodeValue(r)
				if err != nil {
					return nil, fmt.Errorf("%w: %v", ErrInvalidPatch, err)
				}
				op.Old = ov
			}
		case OpDelete:
			ov, err := decodeValue(r)
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidPatch, err)
			}
			op.Old = ov
		case OpSplice:
			idx, err := r.readUvarint()
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidPatch, err)
			}
			cnt, err := r.readUvarint()
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidPatch, err)
			}
			nn, err := r.readLength(maxArrayMembers, "new")
			if err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidPatch, err)
			}
			op.Index = int(idx)
			op.DeleteCount = int(cnt)
			if uint64(op.Index) != idx || uint64(op.DeleteCount) != cnt {
				return nil, fmt.Errorf("%w: splice field overflow", ErrInvalidPatch)
			}
			op.New = make([]any, nn)
			for j := range op.New {
				op.New[j], err = decodeValue(r)
				if err != nil {
					return nil, fmt.Errorf("%w: %v", ErrInvalidPatch, err)
				}
			}
			op.Removed = make([]any, cnt)
			for j := range op.Removed {
				op.Removed[j], err = decodeValue(r)
				if err != nil {
					return nil, fmt.Errorf("%w: %v", ErrInvalidPatch, err)
				}
			}
		}
	}
	if r.pos != len(data) {
		return nil, fmt.Errorf("%w: %d trailing bytes", ErrInvalidPatch, len(data)-r.pos)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return p, nil
}
