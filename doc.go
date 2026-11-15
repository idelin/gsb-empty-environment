// Package netsec implements the authorization decision layer for a DDS
// access-control plugin.
//
// The plugin verifies permission-document signatures out of band; this
// package consumes the already-verified documents and decides whether a
// participant may publish or subscribe to a given topic on a given domain
// and partition.
//
// Semantics:
//
//   - Rules are evaluated in document and rule order; the first matching
//     allow or deny rule decides. If nothing matches, the request is denied
//     (fail closed).
//   - Topics and partitions use case-sensitive fnmatch-style wildcards
//     (see Fnmatch); domain IDs are exact integer matches.
//   - A document outside its validity window, or whose subject name does not
//     equal the certificate subject, is invalid in its entirety and ignored.
package netsec
