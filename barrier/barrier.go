// Package barrier implements a sealing barrier for multi-partition audit
// streams. A window manifest fixes the partition set and each partition's
// upper sequence bound; the aggregate root is signed only after every
// partition in the manifest supplies a contiguous prefix proof for its
// own range. A missing partition can never be substituted by a newer
// root from another partition.
package barrier

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

var (
	// ErrWindowMismatch is returned when a proof carries a window ID
	// other than the coordinator's current window (generation mixing).
	ErrWindowMismatch = errors.New("barrier: proof belongs to a different window")
	// ErrUnknownPartition is returned when a proof names a partition that
	// is not part of the current window manifest.
	ErrUnknownPartition = errors.New("barrier: partition not in window manifest")
	// ErrBaseMismatch is returned when a proof chains from a base root
	// other than the one sealed for the previous window.
	ErrBaseMismatch = errors.New("barrier: proof base root does not match previous window")
	// ErrGap is returned when a prefix proof is not a contiguous
	// sequence covering exactly (Base, Bound].
	ErrGap = errors.New("barrier: prefix proof is not contiguous over the required range")
	// ErrDuplicateProof is returned when a partition submits twice in
	// the same window.
	ErrDuplicateProof = errors.New("barrier: partition already submitted a proof for this window")
	// ErrDuplicateSeal is returned when sealing an already sealed window.
	ErrDuplicateSeal = errors.New("barrier: window already sealed")
	// ErrWindowNotSealed is returned when advancing before sealing.
	ErrWindowNotSealed = errors.New("barrier: current window is not sealed")
	// ErrWindowSealed is returned when submitting to a sealed window.
	ErrWindowSealed = errors.New("barrier: window already sealed, no more submissions")
)

// MissingPartitionsError reports which manifest partitions have not yet
// supplied a valid prefix proof.
type MissingPartitionsError struct {
	Partitions []string
}

func (e *MissingPartitionsError) Error() string {
	return fmt.Sprintf("barrier: missing prefix proofs for partitions %v", e.Partitions)
}

// Leaf is one audited record in a partition stream.
type Leaf struct {
	Seq  uint64
	Hash [32]byte
}

// PartitionSpec fixes one partition's position in a window: the proof
// must cover sequences (Base, Bound] and chain from BaseRoot, the root
// sealed for this partition in the previous window (zero at genesis).
type PartitionSpec struct {
	Base     uint64
	Bound    uint64
	BaseRoot [32]byte
}

// Manifest is the immutable sealing contract for one window.
type Manifest struct {
	WindowID   uint64
	Partitions map[string]PartitionSpec
}

// PartitionIDs returns the sorted partition set of the manifest.
func (m Manifest) PartitionIDs() []string {
	ids := make([]string, 0, len(m.Partitions))
	for id := range m.Partitions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// PrefixProof is a partition's submission for one window: the chained
// base root plus the contiguous leaves for (Base, Bound].
type PrefixProof struct {
	WindowID  uint64
	Partition string
	BaseRoot  [32]byte
	Leaves    []Leaf
}

// Root folds the proof into the partition root for the window:
// r = H(r || leaf.Hash) starting from BaseRoot, in sequence order.
func (p PrefixProof) Root() [32]byte {
	r := p.BaseRoot
	for _, l := range p.Leaves {
		r = chainHash(r, l.Hash)
	}
	return r
}

// Validate checks the proof against the manifest: correct window,
// known partition, matching chained base root, and a contiguous
// sequence covering exactly (Base, Bound].
func (p PrefixProof) Validate(m Manifest) error {
	if p.WindowID != m.WindowID {
		return fmt.Errorf("%w: proof window %d, manifest window %d", ErrWindowMismatch, p.WindowID, m.WindowID)
	}
	spec, ok := m.Partitions[p.Partition]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownPartition, p.Partition)
	}
	if p.BaseRoot != spec.BaseRoot {
		return fmt.Errorf("%w: partition %q", ErrBaseMismatch, p.Partition)
	}
	want := spec.Bound - spec.Base
	if uint64(len(p.Leaves)) != want {
		return fmt.Errorf("%w: partition %q needs %d leaves, got %d", ErrGap, p.Partition, want, len(p.Leaves))
	}
	for i, l := range p.Leaves {
		if l.Seq != spec.Base+1+uint64(i) {
			return fmt.Errorf("%w: partition %q expected seq %d, got %d", ErrGap, p.Partition, spec.Base+1+uint64(i), l.Seq)
		}
	}
	return nil
}

// Seal is the signed aggregate root of a sealed window.
type Seal struct {
	WindowID       uint64
	PartitionRoots map[string][32]byte
	AggregateRoot  [32]byte
	Signature      []byte
}

// AggregateRoot derives the window root from the window ID and the
// sorted (partition, root) pairs.
func AggregateRoot(windowID uint64, roots map[string][32]byte) [32]byte {
	h := sha256.New()
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], windowID)
	h.Write(buf[:])
	ids := make([]string, 0, len(roots))
	for id := range roots {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		binary.BigEndian.PutUint16(buf[:], uint16(len(id)))
		h.Write(buf[:2])
		h.Write([]byte(id))
		root := roots[id]
		h.Write(root[:])
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func chainHash(base, leaf [32]byte) [32]byte {
	var buf [64]byte
	copy(buf[:32], base[:])
	copy(buf[32:], leaf[:])
	return sha256.Sum256(buf[:])
}
