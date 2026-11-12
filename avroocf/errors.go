package avroocf

import "errors"

var (
	// ErrNotContainer is returned when the input is not an Avro object
	// container file (missing the four-byte magic prefix).
	ErrNotContainer = errors.New("avroocf: not an avro object container file")

	// ErrSchemaRejected is returned when the writer schema embedded in the
	// header is not present in the caller-supplied fingerprint allowlist.
	ErrSchemaRejected = errors.New("avroocf: writer schema fingerprint not in allowlist")

	// ErrDepthLimit is returned when a schema or a decoded value exceeds the
	// configured nesting depth.
	ErrDepthLimit = errors.New("avroocf: recursion depth limit exceeded")

	// ErrBlockCount is returned when a block count cannot be reconciled with
	// the bytes remaining in the stream.
	ErrBlockCount = errors.New("avroocf: block count inconsistent with remaining bytes")

	// ErrSyncMarker is returned when a block's trailing sync marker does not
	// match the header sync marker.
	ErrSyncMarker = errors.New("avroocf: sync marker mismatch")

	// ErrEnumOutOfRange is returned when an encoded enum ordinal is outside
	// the writer schema's symbol list.
	ErrEnumOutOfRange = errors.New("avroocf: enum ordinal out of range")

	// ErrUnionOutOfRange is returned when an encoded union branch index is
	// outside the writer schema's branch list.
	ErrUnionOutOfRange = errors.New("avroocf: union branch index out of range")

	// ErrCodecUnsupported is returned for an unknown or disabled compression
	// codec declared in the header metadata.
	ErrCodecUnsupported = errors.New("avroocf: unsupported or disabled codec")

	// ErrMalformed is returned for any other protocol-level corruption.
	ErrMalformed = errors.New("avroocf: malformed container")
)
