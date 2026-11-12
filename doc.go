// Package netsec implements a two-sided recovery-channel rebinding
// transaction for an identity host.
//
// # Threat model
//
// When a user replaces the recovery email, the unverified new address must
// never gain account-recovery power. A change therefore requires two
// independent confirmations:
//
//   - the old side, proving control of the currently active channel;
//   - the new side, proving control of the proposed replacement.
//
// Both confirmation tokens bind the same change digest and the same deadline.
// The active channel switches atomically only after both proofs are recorded.
// Cancellation and any later (concurrent) change revoke every proof issued for
// the earlier digest. At every intermediate point the previously active
// channel remains the usable recovery channel, so the account is never left
// without a definite recovery path.
//
// # Usage
//
//	store := netsec.NewMemoryStore()                 // or a Store backed by the host DB
//	manager, _ := netsec.NewManager(store, hmacKey,
//	    netsec.WithTTL(24*time.Hour))
//
//	change, _ := manager.BeginChange(userID, oldChannel, newChannel)
//	// Deliver change.OldToken out-of-band to oldChannel and
//	// change.NewToken to newChannel.
//
//	res, err := manager.Confirm(userID, tokenFromClickedLink)
//	// res.Status is Pending until the other side confirms, then Switched.
//
// Retries caused by lost responses are idempotent. See permutation_test.go
// for the exhaustive event-ordering coverage of out-of-order delivery,
// response loss, cancellation, concurrent second changes and deadline edges.
package netsec
