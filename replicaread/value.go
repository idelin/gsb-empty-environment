package replicaread

import "bytes"

// PayloadKind distinguishes a stored value from a tombstone.
type PayloadKind int

const (
	// ValuePayload is a live value.
	ValuePayload PayloadKind = iota
	// TombstonePayload marks a deleted value. Tombstones participate in
	// version ordering exactly like values.
	TombstonePayload
)

// Payload is the content stored at a replica for a given version.
type Payload struct {
	Kind PayloadKind
	Data []byte
}

// Value builds a live-value payload.
func Value(data []byte) Payload {
	return Payload{Kind: ValuePayload, Data: data}
}

// Tombstone builds a tombstone payload.
func Tombstone() Payload {
	return Payload{Kind: TombstonePayload}
}

// Equal reports whether two payloads carry identical content.
func (p Payload) Equal(o Payload) bool {
	return p.Kind == o.Kind && bytes.Equal(p.Data, o.Data)
}
