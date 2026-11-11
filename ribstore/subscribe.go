package ribstore

import "net/netip"

// Subscription is a handle on a versioned delta stream. Deltas arrive on C
// in strictly increasing version order. The first delta after Subscribe is a
// FullSync snapshot; after SubscribeFrom it is either a merged incremental
// delta, a FullSync snapshot (when the requested version fell out of the
// journal), or nothing (when the subscriber is already up to date).
//
// The channel must be drained promptly: if it stays full the store drops
// the subscription and closes C rather than blocking commits. Size the
// buffer for the expected burst of commits.
type Subscription struct {
	C     chan Delta
	store *Store
}

// Close detaches the subscription and closes C.
func (sub *Subscription) Close() {
	sub.store.mu.Lock()
	if _, ok := sub.store.subs[sub]; ok {
		delete(sub.store.subs, sub)
		close(sub.C)
	}
	sub.store.mu.Unlock()
}

// send delivers a delta without blocking the store. Called with s.mu held.
func (sub *Subscription) send(d Delta) {
	select {
	case sub.C <- d:
	default:
		delete(sub.store.subs, sub)
		close(sub.C)
	}
}

// Subscribe attaches a new subscriber. The first delta on the channel is a
// FullSync snapshot of the current version; subsequent deltas are live
// commits. Snapshot and stream are taken under the same lock, so no commit
// is lost or duplicated between them.
func (s *Store) Subscribe(buf int) *Subscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub := &Subscription{C: make(chan Delta, buf+1), store: s}
	sub.C <- Delta{Version: s.version, FullSync: true, Announced: s.snapshotLocked()}
	s.subs[sub] = struct{}{}
	return sub
}

// SubscribeFrom reattaches a subscriber that remembers the last version it
// applied (0 means it has nothing). Three cases:
//
//   - version == current: nothing to send, live deltas follow.
//   - version still covered by the journal: all commits since then are
//     merged into one incremental delta. Net-zero changes (announce then
//     withdraw, or re-announce of identical state) are dropped, so old
//     routes are never re-sent.
//   - version fell out of the journal (see Compact): a FullSync snapshot
//     replaces the subscriber's table wholesale.
func (s *Store) SubscribeFrom(version uint64, buf int) *Subscription {
	s.mu.Lock()
	defer s.mu.Unlock()
	sub := &Subscription{C: make(chan Delta, buf+1), store: s}
	switch {
	case version >= s.version:
		// Already up to date.
	case version >= s.base:
		if d := s.mergeFrom(version); len(d.Announced)+len(d.Withdrawn) > 0 {
			d.Version = s.version
			sub.C <- d
		}
	default:
		sub.C <- Delta{Version: s.version, FullSync: true, Announced: s.snapshotLocked()}
	}
	s.subs[sub] = struct{}{}
	return sub
}

// mergeFrom folds every journal entry newer than version into a single net
// delta. For each prefix it compares the view the subscriber still holds
// (the first recorded old view) with the newest view; equal states produce
// no output, which is what keeps replays free of stale duplicates.
func (s *Store) mergeFrom(version uint64) Delta {
	type acc struct {
		old, new *RouteView
	}
	merged := make(map[netip.Prefix]*acc)
	var order []netip.Prefix
	for _, e := range s.journal[version-s.base:] {
		for _, c := range e.changes {
			a, ok := merged[c.prefix]
			if !ok {
				a = &acc{old: c.old}
				merged[c.prefix] = a
				order = append(order, c.prefix)
			}
			a.new = c.new
		}
	}
	var d Delta
	for _, p := range order {
		a := merged[p]
		switch {
		case viewsEqual(a.old, a.new):
			// Net no-op for this subscriber: skip.
		case a.new == nil:
			d.Withdrawn = append(d.Withdrawn, p)
		default:
			d.Announced = append(d.Announced, *a.new)
		}
	}
	sortDelta(&d)
	return d
}
