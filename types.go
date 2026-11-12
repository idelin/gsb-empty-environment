package netsec

import (
	"crypto/sha256"
	"encoding/binary"
)

// WindowID identifies a sealing time window. Window IDs increase
// monotonically; each ID names exactly one sealed generation.
type WindowID uint64

// PartitionID identifies one audit-stream partition inside a window.
type PartitionID string

// Seq is a 1-based audit record sequence number within a partition and
// window. The range 1..UpperBound is the prefix that must be proven.
type Seq uint64

// Digest is a SHA-256 content digest.
type Digest [sha256.Size]byte

// domain separators make the canonical encodings of distinct structures
// incomparable, so a seal digest can never be mistaken for a root digest.
const (
	domainSeal     byte = 0x01
	domainRoot     byte = 0x02
	domainManifest byte = 0x03
)

// writeUint appends a fixed-width big-endian uint64 to dst.
func writeUint(dst []byte, v uint64) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	return append(dst, buf[:]...)
}

// writeBytes appends a length-prefixed byte slice to dst.
func writeBytes(dst, b []byte) []byte {
	dst = writeUint(dst, uint64(len(b)))
	return append(dst, b...)
}

// writeString appends a length-prefixed string to dst.
func writeString(dst []byte, s string) []byte {
	return writeBytes(dst, []byte(s))
}

// PartitionSpec is one entry of a window manifest: a partition and the
// inclusive sequence-number upper bound it must prove for the window.
// Anchor is the trusted digest the partition's first seal (seq 1) chains
// onto; it is the zero digest for a partition's very first window.
type PartitionSpec struct {
	Partition  PartitionID
	UpperBound Seq
	Anchor     Digest
}

// Manifest is the immutable description of a sealing window: the fixed
// partition set and each partition's sequence upper bound.
type Manifest struct {
	Window     WindowID
	Partitions []PartitionSpec
	// PrevRoot anchors the new window onto the signed root of the previous
	// window. It is the zero digest for the genesis window.
	PrevRoot Digest
}

// snapshot is the manifest in a normalized, lookup-friendly form.
type snapshot struct {
	window WindowID
	prev   Digest
	order  []PartitionID
	specs  map[PartitionID]PartitionSpec
}

// compile validates and normalizes a manifest. It copies the partition slice
// so later mutation by the caller cannot change the barrier's view.
func (m Manifest) compile() (*snapshot, error) {
	if len(m.Partitions) == 0 {
		return nil, ErrInvalidManifest
	}
	snap := &snapshot{
		window: m.Window,
		prev:   m.PrevRoot,
		specs:  make(map[PartitionID]PartitionSpec, len(m.Partitions)),
	}
	for _, spec := range m.Partitions {
		if spec.Partition == "" {
			return nil, ErrInvalidManifest
		}
		if _, dup := snap.specs[spec.Partition]; dup {
			return nil, ErrInvalidManifest
		}
		snap.specs[spec.Partition] = spec
		snap.order = append(snap.order, spec.Partition)
	}
	// Deterministic ordering makes every derived digest canonical.
	sortPartitions(snap.order)
	return snap, nil
}

// Digest returns the canonical digest of the manifest. Two manifests with
// identical window, previous root and partition specs always hash equal.
func (m Manifest) Digest() (Digest, error) {
	snap, err := m.compile()
	if err != nil {
		return Digest{}, err
	}
	return snap.digest(), nil
}

func (s *snapshot) digest() Digest {
	h := sha256.New()
	h.Write([]byte{domainManifest})
	h.Write(writeUint(nil, uint64(s.window)))
	h.Write(s.prev[:])
	h.Write(writeUint(nil, uint64(len(s.order))))
	for _, id := range s.order {
		spec := s.specs[id]
		h.Write(writeString(nil, string(id)))
		h.Write(writeUint(nil, uint64(spec.UpperBound)))
		h.Write(spec.Anchor[:])
	}
	return Digest(h.Sum(nil))
}

func sortPartitions(ids []PartitionID) {
	// insertion sort keeps the slice deterministic without importing sort
	// for a typically small partition set.
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j-1] > ids[j]; j-- {
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}
}
