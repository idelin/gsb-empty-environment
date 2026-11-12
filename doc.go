// Package netsec implements the TLS 1.3 record-layer state machine needed to
// process KeyUpdate messages safely.
//
// The state machine owns the read and write traffic secrets, derives the
// successor secret with HKDF-Expand-Label(secret, "traffic upd", "", Hash.Length),
// rotates AEAD keys, resets record sequence numbers on key changes, and tracks
// a sliding-window budget for authenticated inbound KeyUpdate messages.
//
// KeyUpdate messages are rejected unless they occupy exactly one record and
// one complete handshake message. Records that fail AEAD authentication never
// advance the read sequence number, key epoch, or update budget.
package netsec
