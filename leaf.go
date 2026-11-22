package disclosure

// Kind identifies how a leaf participates in the append-only log.
type Kind uint8

const (
	// KindEvent is the first version of a logical record.
	KindEvent Kind = iota
	// KindCorrection is a later version. It references an earlier version and
	// replaces that record's effective tenant and event-time interval.
	KindCorrection
	// KindTombstone is an authorization-bearing erasure marker. It references
	// a version but does not create a new version and never changes its Seq.
	KindTombstone
)

func (k Kind) String() string {
	switch k {
	case KindEvent:
		return "event"
	case KindCorrection:
		return "correction"
	case KindTombstone:
		return "tombstone"
	default:
		return "unknown"
	}
}

// Leaf is one globally appended log entry.
//
// Seq is immutable and fixed by append order. Tenant, EventStart, and EventEnd
// are the effective metadata supplied by this version; for a correction they
// may differ from the referenced version. EventStart and EventEnd are
// inclusive bounds. BodyCommitment commits to the body and remains verifiable
// when Body is withheld or erased. PrevDigest commits to the immediately
// referenced version for corrections and tombstones.
type Leaf struct {
	Seq            uint64  `json:"seq"`
	Kind           Kind    `json:"kind"`
	Tenant         string  `json:"tenant"`
	EventStart     int64   `json:"event_start"`
	EventEnd       int64   `json:"event_end"`
	BodyCommitment Hash    `json:"body_commitment"`
	PrevDigest     Hash    `json:"prev_digest"`
	RefSeq         *uint64 `json:"ref_seq,omitempty"`
	Authorization  []byte  `json:"authorization,omitempty"`
	Body           []byte  `json:"body"`
}

// Digest hashes the immutable metadata of a leaf. The disclosed Body is not
// part of the digest; it is checked against BodyCommitment instead.
func (l Leaf) Digest() Hash {
	var e encoder
	e.str("audit-leaf/v1")
	e.u64(l.Seq)
	e.byte(byte(l.Kind))
	e.str(l.Tenant)
	e.i64(l.EventStart)
	e.i64(l.EventEnd)
	e.hash(l.BodyCommitment)
	e.hash(l.PrevDigest)
	if l.RefSeq == nil {
		e.bool(false)
	} else {
		e.bool(true)
		e.u64(*l.RefSeq)
	}
	e.raw(l.Authorization)
	return HashBytes(e.buf)
}

// Seal computes BodyCommitment from a present body. If a commitment is already
// present, Seal verifies that it matches the body. Withholding the body later
// does not change the leaf digest.
func (l *Leaf) Seal() error {
	if l == nil {
		return ErrInvalidLeaf
	}
	if l.Body == nil {
		return nil
	}
	commitment := CommitBody(l.Body)
	if !l.BodyCommitment.IsZero() && l.BodyCommitment != commitment {
		return ErrBodyCommitment
	}
	l.BodyCommitment = commitment
	return nil
}

// VerifyBody checks a disclosed body against its commitment.
func (l Leaf) VerifyBody() error {
	if l.Body == nil {
		return nil
	}
	if l.BodyCommitment.IsZero() || CommitBody(l.Body) != l.BodyCommitment {
		return ErrBodyCommitment
	}
	return nil
}

// SeqRef is a convenience for constructing a reference to seq.
func SeqRef(seq uint64) *uint64 {
	p := seq
	return &p
}

func validateLeafShape(l Leaf) error {
	if l.Kind > KindTombstone {
		return ErrInvalidLeaf
	}
	if l.Kind != KindTombstone {
		if l.Tenant == "" || l.EventStart > l.EventEnd {
			return ErrInvalidLeaf
		}
	}
	switch l.Kind {
	case KindEvent:
		if l.RefSeq != nil || !l.PrevDigest.IsZero() || len(l.Authorization) != 0 {
			return ErrInvalidLeaf
		}
	case KindCorrection:
		if l.RefSeq == nil || l.PrevDigest.IsZero() || len(l.Authorization) != 0 {
			return ErrInvalidLeaf
		}
	case KindTombstone:
		if l.RefSeq == nil || l.PrevDigest.IsZero() || len(l.Authorization) == 0 {
			return ErrInvalidLeaf
		}
	}
	return l.VerifyBody()
}
