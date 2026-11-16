// Package netsec implements a durable commit-reveal lottery for a frozen set
// of participants.
//
// A round has at most MaxParticipants participants and a host-supplied
// deadline. Each participant commits to
//
//	SHA256(uint32be(len(round)) || round ||
//	       uint32be(len(participant)) || participant ||
//	       nonce)
//
// where nonce is exactly NonceSize bytes. The host accepts at most one
// commitment and one nonce per participant. Reveals are accepted only after
// every participant has committed. Once every reveal has been verified, the
// round result is
//
//	SHA256(nonce[p0] || nonce[p1] || ...)
//
// with participants p0, p1, ... sorted by participant ID.
//
// # Durability and time
//
// Every state transition is derived from an event appended to a Journal. The
// journal Append call must return only after the record is durable. A Host
// applies an event to memory only after Append succeeds, so a failed append
// leaves both the in-memory state and the acknowledged history unchanged.
// Opening a Host replays the journal and reconstructs the state.
//
// The caller supplies a time.Time for every operation. Callers should obtain
// those values from time.Now, which carries Go's monotonic clock reading; the
// host compares them with the round deadline and never reads a wall clock
// itself. A deadline is expired when now is not strictly before Deadline, so
// a reveal at exactly the deadline is late. A timeout aborts the whole round;
// missing nonces are never replaced with zeroes.
//
// # Security limitation
//
// Commit-reveal cannot stop a participant from refusing to reveal. In
// particular, the participant who reveals last can see every other commitment
// and choose to abort the round. Any participant withholding a nonce has the
// same effect. This protocol detects and aborts such a round, but it cannot
// make the random output available. Preventing selective abort requires an
// additional mechanism outside this package, such as time-lock encryption, a
// threshold scheme, penalties, or a trusted party.
//
// The caller must also authenticate that a message really came from the
// participant ID it names. Without authenticated transport, an attacker can
// submit a first commitment on behalf of another participant and block that
// participant's real commitment.
package netsec
