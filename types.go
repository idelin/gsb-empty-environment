package netsec

import "time"

// RecordType identifies a GNS record type. Only PKEY (delegation to another
// zone) and CNAME (alias restart) steer name resolution; all other record
// types are terminal data carried back to the caller.
type RecordType uint32

const (
	// RecordA is placeholder terminal address data; it never steers resolution.
	RecordA RecordType = 1
	// RecordCNAME restarts resolution with an alias name.
	RecordCNAME RecordType = 5
	// RecordPKEY delegates the remaining labels to another zone key.
	RecordPKEY RecordType = 65536
)

// ZonePublicKey is the public key that uniquely identifies a GNS zone.
// A zero value is never returned by the local ego table or a PKEY record.
type ZonePublicKey struct {
	// Key is the opaque zone key material (for example an encoded GNS zkey).
	Key []byte
}

// Equal reports whether two zone keys carry identical key material.
func (z ZonePublicKey) Equal(other ZonePublicKey) bool {
	if len(z.Key) != len(other.Key) {
		return false
	}
	for i := range z.Key {
		if z.Key[i] != other.Key[i] {
			return false
		}
	}
	return true
}

// IsZero reports whether the key carries no material.
func (z ZonePublicKey) IsZero() bool {
	return len(z.Key) == 0
}

// Record is a single GNS record stored under a label inside a zone block.
type Record struct {
	// Type is the GNS record type.
	Type RecordType
	// Value is type-specific payload. For RecordPKEY it is the target zone
	// key; for RecordCNAME it is the absolute alias name to restart with.
	Value []byte
	// Expire is the absolute expiration instant. A zero value means the
	// record never expires; otherwise records at or before now are skipped.
	Expire time.Time
}

// expiredAt reports whether the record must be skipped at instant now.
func (r Record) expiredAt(now time.Time) bool {
	return !r.Expire.IsZero() && !r.Expire.After(now)
}

// Zone is a zone block: the records published under one zone key, grouped by
// label. It is treated as read-only once returned by a ZoneStore.
type Zone struct {
	// Key is the zone key this block belongs to.
	Key ZonePublicKey
	// Records maps each label to the records published under that label.
	Records map[string][]Record
}

// ZoneStore serves zone blocks by zone key. Implementations may be backed by
// a cache, a local namestore or (in production) network fetches.
type ZoneStore interface {
	// Zone returns the zone block for key, or (nil, false) when it is not
	// available. The returned zone must not be modified by the caller.
	Zone(key ZonePublicKey) (*Zone, bool)
}
