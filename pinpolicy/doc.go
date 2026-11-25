// Package pinpolicy is an importable acceptance verifier for pinned
// signature-policy releases used by offline terminals that migrate from an
// old pinned server trust root to a new one.
//
// The verifier does not answer "does the current connection succeed". It
// exhaustively proves, over a bounded small-integer time horizon, two
// properties against the published certificate and policy schedule:
//
//   - Safety: for every legal online window, every admissible checkpoint,
//     and every local clock skew inside the allowed interval, the terminal
//     can never accept a key that has been revoked, a rollback policy whose
//     sequence number is already obsolete, or a migration that lacks the
//     old-root plus new-root handover authorization for the same host, the
//     same new policy digest and every handover role.
//   - Liveness: every legal online window (a window in which a migration
//     target is actually fetchable and some checkpoint can reach the window
//     under the offline bound) contains at least one verifiable migration
//     chain: consecutive sequence numbers, matching predecessor digests,
//     role thresholds satisfied by live keys, and a valid handover at the
//     root-switch step.
//
// # Time model
//
// Time is a discrete small integer. All boundaries are explicit:
//
//   - Release publishing is closed at PublishAt (a release is fetchable when
//     real time t satisfies t >= PublishAt).
//   - Policy windows are closed on both sides: ValidFrom <= localTime <=
//     ValidUntil.
//   - Key revocation is open: a key with Revoked=true is live at real time t
//     when t < RevokedAt. A key revoked exactly when the terminal comes
//     online must therefore be rejected at that exact point.
//   - Handover certificates are signed messages; their authorizing root keys
//     must be live at the real online time.
//
// The real timeline is split at every event endpoint into singleton points
// (Point) and open intervals between two consecutive distinct endpoints
// (Open). A Point keeps its exact timestamp; an Open remembers its endpoints
// and is sampled at an interior integer. Open intervals between two adjacent
// integers contain no integer and are recorded as empty rather than sampled.
// This keeps equal endpoints (closed/open decisions at the exact event time)
// distinct from open-interior behavior.
//
// # Clock and offline model
//
// At a real online time t a terminal reads local time t + s for every
// integer skew s in [SkewMin, SkewMin]. A checkpoint c is viable at t when
// 0 <= t-c <= OfflineMax: the terminal checked the chain at c and has been
// offline no longer than the bound. Anything not yet published at the real
// online time is invisible, regardless of the local clock.
//
// # Rollback rule
//
// A release is obsolete at online time t when another release for the same
// host with a strictly greater sequence number is fetchable. Any signature
// carried by an obsolete release that uses a backup key is a forbidden
// backup-key rollback attempt. Acceptance therefore always follows the
// highest published sequence numbers; a backup key can never revive a
// superseded policy.
package pinpolicy
