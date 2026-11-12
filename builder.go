package taskpkg

import (
	"crypto/ed25519"
)

// RoundBuilder assembles a fully signed update round. The same authorized
// master key signs every segment of a round.
type RoundBuilder struct {
	zone   ZoneID
	start  Version
	target Version
	signer ed25519.PrivateKey

	operations [][]Operation
}

// NewRoundBuilder starts a round that advances zone from start to target.
// target must be greater than start and signer must be a valid ed25519
// private key.
func NewRoundBuilder(zone ZoneID, start, target Version, signer ed25519.PrivateKey) (*RoundBuilder, error) {
	if len(signer) != ed25519.PrivateKeySize {
		return nil, ErrPrivateKey
	}
	if target <= start {
		return nil, ErrVersionMismatch
	}
	return &RoundBuilder{
		zone:   zone,
		start:  start,
		target: target,
		signer: signer,
	}, nil
}

// AddSegment appends a segment carrying the supplied ordered operations. At
// least one segment must be added before Build.
func (b *RoundBuilder) AddSegment(ops []Operation) error {
	for _, op := range ops {
		if op.Type != OpUpsert && op.Type != OpDelete {
			return ErrInvalidSegment
		}
		if op.Key == "" {
			return ErrInvalidSegment
		}
	}
	copied := make([]Operation, len(ops))
	copy(copied, ops)
	b.operations = append(b.operations, copied)
	return nil
}

// Build produces the signed segment chain. Segment digests are chained through
// PrevDigest, the round digest binds every segment body, and the final segment
// additionally signs that round digest and the segment count.
func (b *RoundBuilder) Build() ([]Segment, error) {
	if len(b.signer) != ed25519.PrivateKeySize {
		return nil, ErrPrivateKey
	}
	if len(b.operations) == 0 {
		return nil, ErrEmptyRound
	}

	size := uint64(len(b.operations))
	segments := make([]Segment, 0, len(b.operations))

	var prev [32]byte
	for index, ops := range b.operations {
		seg := Segment{
			Index:         uint64(index),
			Zone:          b.zone,
			StartVersion:  b.start,
			TargetVersion: b.target,
			PrevDigest:    prev,
			Operations:    append([]Operation(nil), ops...),
		}
		prev = segmentDigest(seg)
		segments = append(segments, seg)
	}

	round := roundDigest(b.zone, b.start, b.target, segments)
	segments[len(segments)-1].Final = true
	segments[len(segments)-1].RoundDigest = round
	segments[len(segments)-1].RoundSize = size

	for index := range segments {
		seg := &segments[index]
		seg.Signature = ed25519.Sign(b.signer, segmentSigningInput(*seg))
	}

	return segments, nil
}
