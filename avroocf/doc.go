// Package avroocf is a restricted decoder for untrusted Avro object container
// files received on a message bus.
//
// The ingestion pipeline supplies a fingerprint allowlist of writer schemas.
// Open rejects any container whose embedded writer schema is not on that
// list, and enforces bounds on schema nesting, block object counts, block
// byte sizes, decompression output and schema length before values are
// decoded. Negative block counts follow the Avro specification: the absolute
// value is the object count and the following long is the block byte size,
// which is reconciled against the bytes remaining in the stream. Corrupt sync
// markers, out-of-range enum ordinals and out-of-range union branch indices
// are reported as dedicated sentinel errors.
package avroocf
