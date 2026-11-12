// Package netsec provides validation for IPNS records received by a DHT
// node before they are stored or used to replace an existing record.
//
// Only records carrying a V2 signature are accepted. The V2 signature covers
// the DAG-CBOR payload in IpnsEntry.data, and that payload is required to
// agree exactly with the redundant protobuf fields. Signature verification is
// supplied by the caller through VerifyOptions.Verifier.
package netsec
