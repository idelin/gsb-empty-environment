// Package netsec implements an asynchronous P-256 prekey handshake for a
// messaging host. It binds both device identities, the ephemeral key, every
// prekey identifier and a directory attestation into the transcript, then uses
// that transcript as the HKDF salt and as AEAD associated data.
//
// A verified bundle or directory proof only establishes which device published
// the prekeys. It does not establish that the device is online.
package netsec
