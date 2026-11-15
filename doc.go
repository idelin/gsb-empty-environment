// Package netsec implements a durable commit-reveal randomness component for
// small, fixed participant sets.
//
// A round freezes its participant set when it is created. Each participant may
// submit exactly one commitment and later exactly one matching reveal:
//
//	commitment = SHA256(be64(len(round)) || round ||
//	                    be64(len(participant)) || participant ||
//	                    nonce)
//
// The reveal phase starts only after every participant has committed. Once all
// reveals have been verified, the result is:
//
//	SHA256(concat(nonce[p]) for p in sorted participant IDs)
//
// All externally supplied state transitions are written to durable storage
// before they become visible or are acknowledged. Reopening an Engine replays
// the journal and reconstructs the same state. Timeouts are driven by the
// host's monotonic clock through Tick or Advance; a round with any missing (or
// invalid) reveal is aborted in full and never has a result.
//
// # Security boundary
//
// This protocol cannot prevent the last participant from aborting the round by
// withholding their commitment or a valid reveal until the deadline. Every
// participant can always choose to abort rather than produce a result; a
// protocol for a fixed set without a trusted party cannot distinguish that
// from failure. Applications must decide whether an aborted round is retried
// with a fresh round ID. The host must also authenticate the transport sender
// as the participant ID it supplies; this package binds messages to a round
// and participant but does not provide an identity layer.
package netsec
