package newsindex

import (
	"sort"
	"sync"
)

// Changes returns the principal-visible notifications for commits after
// since. Only the final pre-commit and post-commit visible views are compared,
// so intermediate states inside a batch are never observable. Commits with no
// visible difference produce no notification.
func (a *Archive) Changes(user PrincipalID, since uint64) ([]Notification, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if since > a.current.seq {
		return nil, ErrFutureSnapshot
	}
	notifications, _ := a.changesLocked(user, since)
	return notifications, nil
}

func (a *Archive) changesLocked(user PrincipalID, since uint64) ([]Notification, uint64) {
	current := a.current.seq
	if since > current {
		return nil, current
	}

	var notifications []Notification
	for seq := since + 1; seq <= current; seq++ {
		before := a.snapshots[seq-1]
		after := a.snapshots[seq]
		if before == nil || after == nil {
			continue
		}
		beforeView := buildView(before, user)
		afterView := buildView(after, user)
		changes := diffViews(beforeView, afterView)
		if len(changes) == 0 {
			continue
		}
		notifications = append(notifications, Notification{
			AuthGeneration: afterView.authGeneration,
			ViewToken:      afterView.token,
			Changes:        changes,
		})
	}
	return notifications, current
}

func diffViews(before, after *materializedView) []Change {
	ids := make(map[MessageID]struct{}, len(before.items)+len(after.items))
	for _, message := range before.items {
		ids[message.ID] = struct{}{}
	}
	for _, message := range after.items {
		ids[message.ID] = struct{}{}
	}

	ordered := make([]MessageID, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })

	changes := make([]Change, 0)
	for _, id := range ordered {
		beforeIndex, beforeOK := before.index[id]
		afterIndex, afterOK := after.index[id]
		switch {
		case !beforeOK && afterOK:
			message := cloneMessage(after.items[afterIndex])
			changes = append(changes, Change{Kind: ChangeAdded, ID: id, Message: &message})
		case beforeOK && !afterOK:
			changes = append(changes, Change{Kind: ChangeRemoved, ID: id})
		case beforeOK && afterOK:
			beforeMessage := before.items[beforeIndex]
			afterMessage := after.items[afterIndex]
			if !equalMessageView(beforeMessage, afterMessage) {
				message := cloneMessage(afterMessage)
				changes = append(changes, Change{Kind: ChangeUpdated, ID: id, Message: &message})
			}
		}
	}
	return changes
}

func equalMessageView(a, b MessageView) bool {
	if a.ID != b.ID ||
		a.ReplyCount != b.ReplyCount ||
		a.ReferencedByCount != b.ReferencedByCount ||
		len(a.Fragments) != len(b.Fragments) ||
		len(a.Replies) != len(b.Replies) ||
		len(a.ReferencedBy) != len(b.ReferencedBy) {
		return false
	}
	for i := range a.Fragments {
		if a.Fragments[i] != b.Fragments[i] {
			return false
		}
	}
	for i := range a.Replies {
		if a.Replies[i] != b.Replies[i] {
			return false
		}
	}
	for i := range a.ReferencedBy {
		if a.ReferencedBy[i] != b.ReferencedBy[i] {
			return false
		}
	}
	return true
}

// Subscription polls the same visibility index used by Page, Get, and
// Changes.
type Subscription struct {
	mu      sync.Mutex
	archive *Archive
	user    PrincipalID
	next    uint64
}

// Subscribe creates a notification subscription starting immediately after
// from.
func (a *Archive) Subscribe(user PrincipalID, from uint64) (*Subscription, error) {
	if from > a.CurrentSnapshot() {
		return nil, ErrFutureSnapshot
	}
	return &Subscription{archive: a, user: user, next: from}, nil
}

// Poll returns notifications committed since the previous poll and advances
// the subscription past all versions scanned, including versions with no
// visible difference.
func (s *Subscription) Poll() ([]Notification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.archive.mu.RLock()
	defer s.archive.mu.RUnlock()

	if s.next > s.archive.current.seq {
		return nil, ErrFutureSnapshot
	}
	notifications, latest := s.archive.changesLocked(s.user, s.next)
	s.next = latest + 1
	return notifications, nil
}

// NextSnapshot returns the version that the next Poll will start after.
func (s *Subscription) NextSnapshot() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.next
}
