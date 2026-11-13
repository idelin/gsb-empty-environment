// Package netsec provides a fork-aware deterministic random source for
// security-sensitive token issuance.
//
// A Source generates bytes with an AES-256 counter-mode construction. Its key
// is derived from caller-supplied entropy and, before every read, the Source
// checks a caller-supplied generation identifier (process ID and VM
// generation ID) and a monotonic clock. A changed identifier or a clock
// regression causes a reseed from the old generator state, fresh entropy, the
// new generation identifier, and the clock reading. Buffered bytes from the
// old generator are discarded during the reseed, and concurrent readers wait
// until the reseed completes.
//
// The generator is deterministic by design, so it must not be persisted,
// cloned, or shared between processes without a generation change. Entropy
// failures are reported to the caller; a Source never falls back to output
// from stale state after such a failure.
package netsec
