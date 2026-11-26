// Package apkverify parses APK container bytes and enforces APK signing-block
// coverage before an APK is admitted by a distribution gateway.
//
// Parse derives every offset from the supplied byte slice. It rejects
// polyglot prefixes, ZIP64 markers, contradictory local and central
// directories, duplicate names, overlapping records, malformed EOCD
// comments, trailing bytes, and malformed APK Signing Blocks.
//
// Validate additionally applies the v2-or-newer SDK policy and invokes a
// caller-supplied SignatureVerifier with the exact section 1, central
// directory, and normalized EOCD digest input.
package apkverify
