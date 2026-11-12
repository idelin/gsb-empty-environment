// Package netsec implements the control-plane pre-filter for WPA3-SAE
// Authentication frames.
//
// The filter is intended to run before the SAE state machine. HandleCommit
// performs cheap checks first and returns one of three decisions:
//
//   - ActionAllow: the commit is syntactically and cryptographically safe
//     enough to hand to the SAE state machine.
//   - ActionToken: the AP must reply with an anti-clogging token and must not
//     start an SAE transaction yet.
//   - ActionDrop: the frame is invalid, reflected, or otherwise must be
//     discarded without a response.
//
// A caller that receives ActionAllow must call Complete (or Abort) when the
// SAE exchange finishes. The filter keeps the number of incomplete
// transactions bounded; once that bound is reached, new stations receive an
// anti-clogging token instead of consuming curve arithmetic.
//
// Commit validation enforces the 802.11 SAE scalar range [2, r-1] and
// rejects infinite, off-curve, and reflected elements. Tokens are HMACs made
// with a periodically rotated key and bound to a nonce, the station MAC, and
// the SAE group.
package netsec
