// Package netsec provides a tamper-evident, line-oriented audit log encoder
// for Go services.
//
// User-controlled data is serialized as canonical JSON fields, with newlines,
// control characters, Unicode line separators and JSON delimiters escaped. The
// timestamp, subject, event ID, sequence number and digest-link are generated
// server-side and cannot be overridden by field names in the payload. Each
// record is linked to the previous digest; Verify recomputes the complete
// chain, and VerifyCount/VerifySealed compare a trusted record count and head
// so truncation is detected.
package netsec
