// Package revocation implements a fail-closed subscription to a credential
// revocation log.
//
// A revocation service publishes two kinds of traffic:
//
//   - ordered [Event] values carrying a strict (generation, sequence)
//     coordinate;
//   - authoritative [Checkpoint] values announcing the high-water mark that
//     has been durably published.
//
// A subscriber only trusts its local view once it has applied a contiguous
// run of events up to the latest checkpoint. Any discontinuity (a missing
// sequence, an out-of-order event or a generation rollover) forces the
// subscriber into a denying gap state and requests a full [Snapshot]. While
// the snapshot is in flight, events are buffered. When the snapshot arrives
// it is merged with the buffered increments and committed as one atomic
// switch: callers never observe a partially rebuilt revocation set.
//
// All credential judgments are fail-closed: [Subscriber.Valid] reports
// false (and [Subscriber.IsRevoked] reports true) whenever the subscriber is
// not provably caught up, including right after a restart.
//
// Typical usage:
//
//	store := revocation.NewFileStore("/var/lib/myapp/revocation-state.json")
//	sub, err := revocation.NewSubscriber(store)
//	if err != nil {
//		return err
//	}
//	ctx, cancel := context.WithCancel(context.Background())
//	defer cancel()
//	subscription := revocation.Run(ctx, sub, client)
//	defer func() { _ = subscription.Wait() }()
//
//	if !sub.Valid(credentialID) {
//		// deny access: revoked, or local view not trustworthy
//	}
package revocation
