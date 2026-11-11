// Package imapsync implements incremental IMAP folder synchronization for a
// mail collector. The transport is a plain io.ReadWriter, responses are
// parsed with full support for literals (which may span multiple underlying
// reads), quoted strings and parenthesized lists. Synchronization uses
// UIDVALIDITY to detect mailbox rebuilds and CONDSTORE MODSEQ to pull flag
// changes incrementally, while tolerating EXPUNGE and new arrivals mid-sync.
// Per-folder state is persisted atomically.
package imapsync
