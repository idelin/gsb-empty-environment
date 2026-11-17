// Package netsec provides structural admission checks for ISO 8583 messages
// received by an acquirer gateway.
//
// The package deliberately does not decode or retain field values. Callers
// supply the field table and the message types that may carry an encrypted
// PIN block (field 52). Admission either returns field offsets for the
// caller's decoder or a redacted structural error.
package netsec
