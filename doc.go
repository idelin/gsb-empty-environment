// Package netsec provides a transport-host receiver for datagrams that carry
// a key phase bit and a truncated packet number.
//
// # Number reassembly
//
// Each datagram header holds one key phase bit followed by a packet number
// truncated to a negotiated width (1..4 bytes). The full packet number is the
// legal candidate nearest to the highest authenticated number plus one; when
// two candidates are equally near the smaller one is chosen.
//
// # Key generation gating
//
// The receiver keeps the current generation key and, for a short period, the
// immediately previous generation key. A provisioned next key only becomes
// active once the first datagram carrying its phase bit authenticates. Forged
// datagrams can never advance the phase: the phase, highest number and replay
// window all move only after the host Authenticator returns nil.
//
// Previous-generation keys authenticate only numbers inside the single
// truncation window ending at the switch boundary. A number from before that
// window, including a same-phase replay after two wraps, is rejected with
// ErrBeforeSwitch rather than being read under the stale key.
//
// # Concurrency
//
// A Receiver is safe for concurrent use. Receive serializes access internally,
// so concurrent copies of the same datagram are accepted exactly once and the
// Authenticator is invoked at most once for them. The host Authenticator must
// be safe for concurrent use and must not call back into the same Receiver
// while its Authenticate method is running.
package netsec
