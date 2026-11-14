// Package netsec provides an importable field policy component for a Go
// message service boundary.
//
// Each message version declares its known fields and reserved field numbers
// via VersionSchema. A Sanitizer validates field numbers, value types and
// repetition counts, then applies an UnknownPolicy to fields that are neither
// known nor reserved: reject, discard, or quarantine them away from the
// downstream-readable view. Cross-version traffic moves only through an
// explicit ConversionPlan, so field numbers reused between versions can never
// be implicitly forwarded with a changed meaning.
package netsec
