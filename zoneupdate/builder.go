package zoneupdate

import "crypto/ed25519"

// Builder constructs a signed update round for a zone. Master nodes use
// it to produce chains that pass Service.Submit.
type Builder struct {
	zone     string
	baseVer  uint64
	key      ed25519.PrivateKey
	segs     []*Segment
	finished bool
}

// NewBuilder starts a round for zone anchored at baseVer, which must be
// the zone's current version for the round to be accepted.
func NewBuilder(zone string, baseVer uint64, key ed25519.PrivateKey) *Builder {
	return &Builder{zone: zone, baseVer: baseVer, key: key}
}

// AddSegment appends a segment carrying ops to the round.
func (b *Builder) AddSegment(ops ...Op) *Builder {
	if b.finished {
		panic("zoneupdate: Build already called")
	}
	seq := uint64(len(b.segs)) + 1
	seg := &Segment{
		Zone:      b.zone,
		Seq:       seq,
		StartVer:  b.baseVer + seq - 1,
		TargetVer: b.baseVer + seq,
		Ops:       append([]Op(nil), ops...),
	}
	if n := len(b.segs); n > 0 {
		seg.PrevDigest = b.segs[n-1].contentDigest()
	}
	b.segs = append(b.segs, seg)
	return b
}

// Build seals the round: the last segment becomes the final segment
// committing to the round digest and segment count, then every segment
// is signed. It returns the segments in chain order.
func (b *Builder) Build() []*Segment {
	if b.finished {
		panic("zoneupdate: Build already called")
	}
	b.finished = true
	if len(b.segs) == 0 {
		return nil
	}
	last := b.segs[len(b.segs)-1]
	last.Final = true
	last.SegCount = uint64(len(b.segs))
	last.RoundDigest = roundDigest(b.zone, b.baseVer, b.segs)
	for _, seg := range b.segs {
		seg.Signature = ed25519.Sign(b.key, seg.signingPayload())
	}
	return b.segs
}
