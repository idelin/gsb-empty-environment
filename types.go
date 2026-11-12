package taskpkg

// ZoneID identifies a name-service zone. It is bound into every signature so a
// segment cannot be replayed into another zone.
type ZoneID string

// Version is a monotonically increasing zone version. An update round starts at
// one version and publishes the next.
type Version uint64

// Record is a single name-service record identified by a zone-scoped key.
type Record struct {
	Key   string
	Value []byte
}

// Digest returns the canonical SHA-256 digest of the record's key and value.
// Delete operations must present this exact digest for the record they remove.
func (r Record) Digest() [32]byte {
	return RecordDigest(r.Key, r.Value)
}

// OpType distinguishes upserts from deletes.
type OpType uint8

const (
	// OpUpsert creates or replaces a record.
	OpUpsert OpType = 1
	// OpDelete removes a record only if its current digest matches Digest.
	OpDelete OpType = 2
)

// Operation is one entry in a segment's ordered operation sequence.
type Operation struct {
	Type   OpType
	Key    string
	Value  []byte
	Digest [32]byte
}

// Upsert builds an operation that creates or replaces the record under key.
func Upsert(key string, value []byte) Operation {
	return Operation{
		Type:  OpUpsert,
		Key:   key,
		Value: append([]byte(nil), value...),
	}
}

// Delete builds an operation that only succeeds when the live record under key
// has the supplied digest.
func Delete(key string, digest [32]byte) Operation {
	return Operation{
		Type:   OpDelete,
		Key:    key,
		Digest: digest,
	}
}

// Segment is one signed link of an update chain.
//
// The signature covers the zone, StartVersion, TargetVersion, the digest of the
// previous segment (zero for the first segment), the ordered operations and,
// for the final segment, the round digest and segment count. See signing.go
// for the exact canonical encoding.
type Segment struct {
	Index         uint64
	Zone          ZoneID
	StartVersion  Version
	TargetVersion Version
	PrevDigest    [32]byte
	Operations    []Operation

	// Final marks the closing segment. Only the final segment carries a
	// round digest and segment count.
	Final       bool
	RoundDigest [32]byte
	RoundSize   uint64

	Signature []byte
}

// Digest returns the SHA-256 digest of the segment's unsigned canonical body,
// i.e. everything covered by the signature except the signature itself and the
// final-segment round fields. Chaining and the round digest are defined over
// this value.
func (s *Segment) Digest() [32]byte {
	return segmentDigest(*s)
}

// SigningInput returns the exact canonical bytes that Signature authenticates.
// It is exported so callers and tests can inspect the context-bound payload.
func (s *Segment) SigningInput() []byte {
	return segmentSigningInput(*s)
}
