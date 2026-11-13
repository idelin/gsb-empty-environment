// Package netsec implements an A/B firmware update state machine with a
// monotonic, trusted rollback-index fuse and rollback-tolerant ordinary
// persistent state.
//
// Ordinary state may be replaced by an old snapshot after a crash. The state
// machine therefore never treats ordinary state as an authority for the
// rollback index. Every image that is about to be started must have been
// authenticated by the host's manifest verifier and must not be below the
// rollback index read from the fuse.
package netsec
