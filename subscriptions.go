package netsec

import (
	"fmt"
	"sync"
)

// Subscription is the handle returned to the broker for an in-flight
// subscription. It remembers the ACL generation it was opened under so that
// later rule changes force a fresh check rather than trusting the old result.
type Subscription struct {
	mu         sync.Mutex
	principal  string
	topic      Resource
	group      Resource
	generation uint64
}

// OpenSubscription authorizes and opens a subscription on topic using group.
// Both resources must be well formed and the principal must hold ActionRead on
// the topic and ActionAdmin on the group under the same ACL generation. When a
// group name is supplied it must already be bound to the same principal.
func (a *Authorizer) OpenSubscription(principal, topicName, groupName string) (*Subscription, error) {
	if principal == "" {
		return nil, ErrEmptyPrincipal
	}
	topic := Resource{Kind: KindTopic, Name: topicName}
	if err := topic.Valid(a.delim); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotAuthorized, err)
	}
	group := Resource{Kind: KindGroup, Name: groupName}
	if groupName != "" {
		if err := group.Valid(a.delim); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrNotAuthorized, err)
		}
	}

	a.mu.RLock()
	snap := a.current
	if groupName != "" {
		owner, ok := a.groups[groupName]
		if !ok || owner != principal {
			a.mu.RUnlock()
			if !ok {
				return nil, fmt.Errorf("%w: group %q", ErrGroupNotFound, groupName)
			}
			return nil, fmt.Errorf("%w: group %q is owned by %q", ErrGroupOwnerConflict, groupName, owner)
		}
	}
	a.mu.RUnlock()

	if err := a.authorizeAgainst(snap, principal, topic, ActionRead); err != nil {
		return nil, err
	}
	if groupName != "" {
		if err := a.authorizeAgainst(snap, principal, group, ActionAdmin); err != nil {
			return nil, err
		}
	}

	return &Subscription{
		principal:  principal,
		topic:      topic,
		group:      group,
		generation: snap.generation,
	}, nil
}

// Generation reports the ACL generation the subscription was opened under.
func (s *Subscription) Generation() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation
}

// Topic returns the subscribed topic resource.
func (s *Subscription) Topic() Resource {
	return s.topic
}

// Group returns the consumer group resource used by the subscription. Its
// Name is empty when the subscription has no group.
func (s *Subscription) Group() Resource {
	return s.group
}

// Reverify re-checks an in-flight subscription against the current ACL
// generation. After ReplaceACL commits a new generation, every subscription
// whose stored generation differs must be re-verified before it is allowed to
// keep consuming. A subscription that no longer passes returns
// ErrSubscriptionStale (wrapping the underlying reason) and the broker must
// tear it down; the cache is never consulted to keep an old decision alive.
func (a *Authorizer) Reverify(sub *Subscription) error {
	if sub == nil {
		return fmt.Errorf("%w: nil subscription", ErrNotAuthorized)
	}

	a.mu.RLock()
	snap := a.current
	currentGeneration := snap.generation
	if sub.group.Name != "" {
		owner, ok := a.groups[sub.group.Name]
		if !ok || owner != sub.principal {
			a.mu.RUnlock()
			if !ok {
				return fmt.Errorf("%w: %w", ErrSubscriptionStale, fmt.Errorf("group %q: %w", sub.group.Name, ErrGroupNotFound))
			}
			return fmt.Errorf("%w: %w", ErrSubscriptionStale, ErrGroupOwnerConflict)
		}
	}
	a.mu.RUnlock()

	if err := a.authorizeAgainst(snap, sub.principal, sub.topic, ActionRead); err != nil {
		return fmt.Errorf("%w: %w", ErrSubscriptionStale, err)
	}
	if sub.group.Name != "" {
		if err := a.authorizeAgainst(snap, sub.principal, sub.group, ActionAdmin); err != nil {
			return fmt.Errorf("%w: %w", ErrSubscriptionStale, err)
		}
	}

	sub.mu.Lock()
	sub.generation = currentGeneration
	sub.mu.Unlock()
	return nil
}

// AuthorizeTopic is a convenience wrapper for topic checks.
func (a *Authorizer) AuthorizeTopic(principal, topicName string, actions Action) error {
	return a.Authorize(principal, Resource{Kind: KindTopic, Name: topicName}, actions)
}

// AuthorizeGroup is a convenience wrapper for consumer group checks.
func (a *Authorizer) AuthorizeGroup(principal, groupName string, actions Action) error {
	return a.Authorize(principal, Resource{Kind: KindGroup, Name: groupName}, actions)
}

// AuthorizeTransaction is a convenience wrapper for transaction id checks.
func (a *Authorizer) AuthorizeTransaction(principal, transactionID string, actions Action) error {
	return a.Authorize(principal, Resource{Kind: KindTransaction, Name: transactionID}, actions)
}
