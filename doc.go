// Package netsec provides validation, comparison and in-memory storage of
// IPNS records for a DHT record store.
//
// # Security model
//
// Only the IPNS V2 signature is trusted. A record that carries a legacy V1
// signature is rejected outright, and a record without a V2 signature is
// rejected as well. The cryptographic check itself is injected by the caller
// through [V2Verifier], because key resolution is node-specific; the package
// constructs the exact signed byte string (the libp2p domain-separation
// prefix plus the CBOR payload) and enforces everything around it.
//
// The CBOR payload covered by the V2 signature is treated as the source of
// truth and must match the protobuf fields Value, Validity, ValidityType,
// Sequence and TTL byte for byte. This defeats replay of older sequence
// numbers as well as records whose two copies of the fields disagree.
// Replacement of a stored record follows the IPNS ordering: a strictly larger
// sequence number wins; on equal sequence numbers the later EOL wins; when
// both are equal the incoming record is discarded.
package netsec
