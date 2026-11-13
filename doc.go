// Package netsec implements a record-sequence state machine for a TLS
// record layer. Sequence numbers never wrap: once the 64-bit sequence
// space under one key is exhausted, the connection must rekey or close
// before any further record is accepted.
package netsec
