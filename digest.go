package gsb

import (
	"encoding/binary"
	"hash/crc32"
)

// NumSegments is the number of hash-ring intervals the member ID space
// is split into. Reconciliation only pulls members from divergent
// intervals instead of exchanging the full member list.
const NumSegments = 16

// Digest is an incremental hash-ring checksum over the membership view.
// Each member contributes one 4-byte CRC into its ring interval; interval
// checksums are XOR-combined so inserts/updates/removals are O(1) and do
// not require rehashing the whole view.
type Digest struct {
	segments [NumSegments]uint32
}

func segmentIndex(id string) uint32 {
	return crc32.ChecksumIEEE([]byte(id)) % NumSegments
}

func checksumMember(m Member) uint32 {
	var buf [9]byte
	buf[0] = byte(m.State)
	binary.BigEndian.PutUint64(buf[1:], m.Incarnation)
	h := crc32.NewIEEE()
	h.Write([]byte(m.ID))
	h.Write(buf[:])
	return h.Sum32()
}

// Add folds a member into the digest.
func (d *Digest) Add(m Member) {
	d.segments[segmentIndex(m.ID)] ^= checksumMember(m)
}

// Remove folds a member out of the digest (XOR is its own inverse).
func (d *Digest) Remove(m Member) {
	d.segments[segmentIndex(m.ID)] ^= checksumMember(m)
}

// Sum returns the 4-byte CRC exchanged between peers during periodic
// probes. Equal sums mean the views are (with CRC32 probability) identical.
func (d *Digest) Sum() uint32 {
	var buf [NumSegments * 4]byte
	for i, s := range d.segments {
		binary.BigEndian.PutUint32(buf[i*4:], s)
	}
	return crc32.ChecksumIEEE(buf[:])
}

// DiffSegments returns the ring interval indexes whose checksums differ
// from the peer's digest. These intervals bound the state that must be
// reconciled.
func (d *Digest) DiffSegments(o *Digest) []int {
	var out []int
	for i := range d.segments {
		if d.segments[i] != o.segments[i] {
			out = append(out, i)
		}
	}
	return out
}
