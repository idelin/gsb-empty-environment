// Package netsec implements a restart-safe trust-anchor rollover state machine.
//
// New keys are promoted only after a continuous observation period. The
// observation timeline uses a trusted monotonic nanosecond clock and a
// persisted high-water mark, so a restart or clock rollback cannot shorten the
// hold-down. Revocations require a signature from a currently active trust
// anchor.
package netsec
