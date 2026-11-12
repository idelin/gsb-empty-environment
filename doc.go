// Package netsec provides Double Ratchet receive-chain state management for
// end-to-end encrypted messaging.
//
// A ReceiveChain derives one message key per sequence number, bounds skipped
// keys, and commits state only after the caller's AEAD authentication
// succeeds. See ReceiveChain for the transaction model and SkippedKeyStore
// for the one-shot skipped-key cache.
package netsec
