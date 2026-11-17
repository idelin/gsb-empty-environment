// Package zoneupdate verifies and atomically applies signed incremental
// updates to DNS-style zones on behalf of a name service.
//
// An authorized master node submits a round of segments. Every segment
// signature binds the zone identity, the start and target versions, the
// digest of the previous segment, and the ordered add/delete operations.
// The final segment additionally signs the whole-round digest and the
// segment count, so a chain cannot be truncated, extended, reordered, or
// spliced with segments from another zone or round. A round is only
// published after the entire chain verifies against the current zone
// version and every delete matches the digest of an existing record.
package zoneupdate

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
)

// Digest is a SHA-256 digest used for records, segments, and rounds.
type Digest [sha256.Size]byte

// Record is a single name-service record.
type Record struct {
	Name  string
	RType string
	Value string
}

// RecordDigest returns the canonical digest of a record. Delete
// operations reference records by this digest.
func RecordDigest(r Record) Digest {
	h := sha256.New()
	h.Write([]byte("zoneupdate/record/v1"))
	writeString(h, r.Name)
	writeString(h, r.RType)
	writeString(h, r.Value)
	return sum(h)
}

// OpKind identifies the operation performed by an Op.
type OpKind byte

const (
	// OpAdd adds or replaces a record.
	OpAdd OpKind = iota + 1
	// OpDelete removes the record whose digest is Op.Digest.
	OpDelete
)

// Op is a single add or delete operation within a segment.
type Op struct {
	Kind   OpKind
	Record Record // set for OpAdd
	Digest Digest // set for OpDelete: digest of the old record to remove
}

// Add returns an operation that adds rec to the zone.
func Add(rec Record) Op { return Op{Kind: OpAdd, Record: rec} }

// Delete returns an operation that removes rec, identified by its digest.
func Delete(rec Record) Op { return Op{Kind: OpDelete, Digest: RecordDigest(rec)} }

// DeleteDigest returns an operation that removes the record with digest d.
func DeleteDigest(d Digest) Op { return Op{Kind: OpDelete, Digest: d} }

// Segment is one signed increment within an update round.
type Segment struct {
	Zone       string
	Seq        uint64 // 1-based position within the round
	StartVer   uint64 // zone version before this segment
	TargetVer  uint64 // zone version after this segment (StartVer + 1)
	PrevDigest Digest // content digest of the previous segment, zero for the first
	Ops        []Op

	// Final-only fields. The final segment additionally commits to the
	// whole-round digest and the total segment count.
	Final       bool
	RoundDigest Digest
	SegCount    uint64

	Signature []byte // ed25519 signature over the signing payload
}

// payload returns the canonical encoding of the segment with the round
// digest field set to rd. Every field that binds the segment to its
// context is covered: zone, sequence, versions, previous digest, the
// ordered operations, and (for the final segment) the round commitment.
func (s *Segment) payload(rd Digest) []byte {
	h := sha256.New()
	h.Write([]byte("zoneupdate/segment/v1"))
	writeString(h, s.Zone)
	writeUint64(h, s.Seq)
	writeUint64(h, s.StartVer)
	writeUint64(h, s.TargetVer)
	h.Write(s.PrevDigest[:])
	writeUint64(h, uint64(len(s.Ops)))
	for _, op := range s.Ops {
		h.Write([]byte{byte(op.Kind)})
		switch op.Kind {
		case OpAdd:
			writeString(h, op.Record.Name)
			writeString(h, op.Record.RType)
			writeString(h, op.Record.Value)
		case OpDelete:
			h.Write(op.Digest[:])
		}
	}
	if s.Final {
		h.Write([]byte{1})
		h.Write(rd[:])
		writeUint64(h, s.SegCount)
	} else {
		h.Write([]byte{0})
	}
	return h.Sum(nil)
}

// signingPayload is the exact byte string covered by Signature.
func (s *Segment) signingPayload() []byte { return s.payload(s.RoundDigest) }

// contentDigest identifies a segment within a chain. It covers the full
// signing payload except the round commitment itself, which avoids a
// circular dependency between the final segment and the round digest.
func (s *Segment) contentDigest() Digest {
	return sha256.Sum256(s.payload(Digest{}))
}

// roundDigest commits to the zone, the base version, the segment count,
// and every segment content digest in order.
func roundDigest(zone string, baseVer uint64, segs []*Segment) Digest {
	h := sha256.New()
	h.Write([]byte("zoneupdate/round/v1"))
	writeString(h, zone)
	writeUint64(h, baseVer)
	writeUint64(h, uint64(len(segs)))
	for _, s := range segs {
		d := s.contentDigest()
		h.Write(d[:])
	}
	return sum(h)
}

func writeString(h hash.Hash, s string) {
	writeUint64(h, uint64(len(s)))
	h.Write([]byte(s))
}

func writeUint64(h hash.Hash, v uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	h.Write(buf[:])
}

func sum(h hash.Hash) Digest {
	var d Digest
	h.Sum(d[:0])
	return d
}
