package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// PageSize is the fixed size of a sandbox page in this model.
const PageSize = 64

// PageBytes is an alias for PageSize.
const PageBytes = PageSize

// PageID identifies a virtual page. PhysicalID identifies the backing
// physical page. They are deliberately distinct concepts, but both are
// represented by uint64 so hosts can use their native handles directly.
type (
	PageID     = uint64
	PhysicalID = uint64
)

// Perm is a permission bitmap for a page.
type Perm uint8

// Permissions is an alias for Perm.
type Permissions = Perm

const (
	PermNone Perm = 0

	PermRead    Perm = 1 << 0
	PermWrite   Perm = 1 << 1
	PermExecute Perm = 1 << 2

	PermAll = PermRead | PermWrite | PermExecute
)

// Short aliases for the permission bits.
const (
	R = PermRead
	W = PermWrite
	X = PermExecute

	PermR = PermRead
	PermW = PermWrite
	PermX = PermExecute
)

// CanRead reports whether p contains the read permission.
func (p Perm) CanRead() bool { return p&PermRead != 0 }

// CanWrite reports whether p contains the write permission.
func (p Perm) CanWrite() bool { return p&PermWrite != 0 }

// CanExecute reports whether p contains the execute permission.
func (p Perm) CanExecute() bool { return p&PermExecute != 0 }

func (p Perm) String() string {
	if p == PermNone {
		return "none"
	}
	var b strings.Builder
	if p&PermRead != 0 {
		b.WriteByte('r')
	}
	if p&PermWrite != 0 {
		b.WriteByte('w')
	}
	if p&PermExecute != 0 {
		b.WriteByte('x')
	}
	if b.Len() == 0 {
		return fmt.Sprintf("invalid(%#x)", uint8(p))
	}
	return b.String()
}

// Source identifies the file or mapping that introduced a physical page.
// Name is informational. Digest is the trusted identity of the origin and is
// propagated to every alias and execution identity for the physical page.
type Source struct {
	Name   string
	Digest [32]byte
}

// String returns a stable display name for the source.
func (s Source) String() string {
	if s.Name != "" {
		return s.Name
	}
	if s.Digest != ([32]byte{}) {
		return "sha256:" + hex.EncodeToString(s.Digest[:])
	}
	return ""
}

// WriteInterval is a half-open byte range [Offset, Offset+Length).
type WriteInterval struct {
	Offset int
	Length int
}

// WriteRecord is one write in the provenance chain of a physical page.
// Bytes is a copy of the bytes written by the event.
type WriteRecord struct {
	Sequence           uint64
	PageID             PageID
	VirtualGeneration  uint64
	Offset             int
	Length             int
	Bytes              []byte
	PhysicalGeneration uint64
	SourceDigest       [32]byte
	SourceName         string
}

// Identity is the immutable identity captured when a writable generation is
// made executable. Digest is SHA-256 over exactly Snapshot, which is always
// PageSize bytes. Writing to the physical page after this point invalidates
// the identity, even if the bytes are later restored.
type Identity struct {
	PhysicalID         PhysicalID
	PhysicalGeneration uint64
	PageID             PageID
	VirtualGeneration  uint64
	Generation         uint64
	Digest             [32]byte
	Snapshot           []byte
	Source             Source
	SourceDigest       [32]byte
	SourceName         string
	FileSource         string
	WriteIntervals     []WriteInterval
	WriteChain         []WriteRecord
}

// Verify checks that the identity's digest matches its snapshot.
func (id Identity) Verify() error {
	if len(id.Snapshot) != PageSize {
		return fmt.Errorf("%w: snapshot length %d, want %d", ErrInvalidIdentity, len(id.Snapshot), PageSize)
	}
	if got := sha256.Sum256(id.Snapshot); got != id.Digest {
		return fmt.Errorf("%w: digest does not match snapshot", ErrInvalidIdentity)
	}
	return nil
}

// Execution is the result of an Execute event. Code contains exactly the
// bytes covered by the identity snapshot for the requested range.
type Execution struct {
	Identity

	Offset int
	Length int
	Code   []byte

	Detection    Detection
	Verdict      Verdict
	RuleError    error
	DetectionKey DetectionKey
}

// Verify checks that the executed bytes are covered by the current snapshot
// and that the snapshot still hashes to the reported digest.
func (e *Execution) Verify() error {
	if e == nil {
		return fmt.Errorf("%w: nil execution", ErrInvalidIdentity)
	}
	if err := e.Identity.Verify(); err != nil {
		return err
	}
	if e.Offset < 0 || e.Length <= 0 || e.Offset > PageSize || e.Length > PageSize || e.Offset+e.Length > PageSize {
		return fmt.Errorf("%w: execution range [%d,%d)", ErrOutOfRange, e.Offset, e.Offset+e.Length)
	}
	if len(e.Code) != e.Length {
		return fmt.Errorf("%w: code length %d, want %d", ErrInvalidIdentity, len(e.Code), e.Length)
	}
	if !bytes.Equal(e.Code, e.Snapshot[e.Offset:e.Offset+e.Length]) {
		return fmt.Errorf("%w: executed bytes differ from identity snapshot", ErrInvalidIdentity)
	}
	return nil
}

// PageState is a point-in-time public view of a virtual page.
type PageState struct {
	PageID             PageID
	PhysicalID         PhysicalID
	Permissions        Perm
	VirtualGeneration  uint64
	Generation         uint64
	PhysicalGeneration uint64
	Source             Source
	HasIdentity        bool
}

var (
	ErrNotMapped         = errors.New("netsec: page is not mapped")
	ErrSamePage          = errors.New("netsec: remap target must differ from the new page ID")
	ErrNoPhysical        = errors.New("netsec: physical page does not exist")
	ErrWXConflict        = errors.New("netsec: writable and executable permissions may not coexist")
	ErrInvalidPermission = errors.New("netsec: invalid permission bits")
	ErrPermissionDenied  = errors.New("netsec: operation not permitted by page permissions")
	ErrOutOfRange        = errors.New("netsec: range outside the 64-byte page")
	ErrNoIdentity        = errors.New("netsec: no valid execution identity")
	ErrStaleIdentity     = errors.New("netsec: execution identity is stale")
	ErrInvalidIdentity   = errors.New("netsec: malformed execution identity")
	ErrInvalidSource     = errors.New("netsec: unsupported source value")
	ErrInvalidRule       = errors.New("netsec: unsupported detection rule")
	ErrRuleFailed        = errors.New("netsec: detection rule failed")
)

func checkPermissions(p Perm) error {
	if p&^PermAll != 0 {
		return fmt.Errorf("%w: %#x", ErrInvalidPermission, uint8(p))
	}
	if p&PermWrite != 0 && p&PermExecute != 0 {
		return ErrWXConflict
	}
	return nil
}

func validateRange(offset, length int) error {
	if offset < 0 || length < 0 || offset > PageSize || length > PageSize || offset+length > PageSize {
		return fmt.Errorf("%w: [%d,%d)", ErrOutOfRange, offset, offset+length)
	}
	return nil
}

func normalizeSource(v any) (Source, error) {
	switch s := v.(type) {
	case nil:
		return Source{}, nil
	case Source:
		return s, nil
	case *Source:
		if s == nil {
			return Source{}, nil
		}
		return *s, nil
	case [32]byte:
		return Source{Digest: s}, nil
	case *[32]byte:
		if s == nil {
			return Source{}, nil
		}
		return Source{Digest: *s}, nil
	case []byte:
		if s == nil {
			return Source{}, nil
		}
		if len(s) == 32 {
			var d [32]byte
			copy(d[:], s)
			return Source{Digest: d}, nil
		}
		return Source{Digest: sha256.Sum256(s)}, nil
	case string:
		if s == "" {
			return Source{}, nil
		}
		if len(s) == 64 {
			if raw, err := hex.DecodeString(s); err == nil {
				var d [32]byte
				copy(d[:], raw)
				return Source{Name: s, Digest: d}, nil
			}
		}
		return Source{Name: s, Digest: sha256.Sum256([]byte(s))}, nil
	case fmt.Stringer:
		return normalizeSource(s.String())
	default:
		return Source{}, fmt.Errorf("%w: %T", ErrInvalidSource, v)
	}
}

func cloneIdentity(in Identity) Identity {
	out := in
	out.Snapshot = append([]byte(nil), in.Snapshot...)
	out.WriteIntervals = append([]WriteInterval(nil), in.WriteIntervals...)
	if in.WriteChain != nil {
		out.WriteChain = make([]WriteRecord, len(in.WriteChain))
		for i := range in.WriteChain {
			out.WriteChain[i] = in.WriteChain[i]
			out.WriteChain[i].Bytes = append([]byte(nil), in.WriteChain[i].Bytes...)
		}
	}
	return out
}

func snapshotOf(data *[PageSize]byte) []byte {
	out := make([]byte, PageSize)
	copy(out, data[:])
	return out
}
