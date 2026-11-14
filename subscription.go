package netsec

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

var (
	// ErrSubscriptionClosed means that a subscription was closed locally or
	// revoked by an ACL generation change.
	ErrSubscriptionClosed = errors.New("subscription closed")
)

// DefaultSubscriptionBuffer is the default per-subscription event buffer.
const DefaultSubscriptionBuffer = 256

// Event is a message published to an exchange. Tenant is the producer tenant;
// an event is never delivered to a subscription owned by another tenant.
type Event struct {
	Tenant     string
	Exchange   string
	RoutingKey string
	Payload    any
}

// Subscription is an authorized topic binding owned by one tenant.
type Subscription struct {
	authorizer *Authorizer
	tenant     string
	exchange   string
	binding    string
	pattern    Pattern

	events chan Event
	done   chan struct{}

	mu         sync.Mutex
	generation uint64
	closed     bool
	revoked    bool
	doneOnce   sync.Once
	dropped    uint64
}

// Subscribe validates a tenant, exchange, and binding against the current ACL
// and creates a subscription. Invalid or overly broad bindings are rejected
// before any subscription is registered.
func (a *Authorizer) Subscribe(tenant, exchange, binding string) (*Subscription, error) {
	return a.SubscribeBuffered(tenant, exchange, binding, DefaultSubscriptionBuffer)
}

// Subscribe is an alias for SubscribeBuffered.
func (a *Authorizer) SubscribeBuffered(tenant, exchange, binding string, capacity int) (*Subscription, error) {
	pattern, err := ParsePattern(binding)
	if err != nil {
		return nil, deny(ErrInvalidPattern, "%s", err.Error())
	}
	if capacity < 0 {
		capacity = 0
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	snapshot := a.state.Load()
	if err := snapshot.authorize(tenant, exchange, pattern); err != nil {
		return nil, err
	}

	subscription := &Subscription{
		authorizer: a,
		tenant:     tenant,
		exchange:   exchange,
		binding:    binding,
		pattern:    pattern,
		events:     make(chan Event, capacity),
		done:       make(chan struct{}),
		generation: snapshot.generation,
	}
	a.subs[subscription] = struct{}{}
	return subscription, nil
}

// Unsubscribe closes a subscription and removes it from the authorizer.
func (a *Authorizer) Unsubscribe(subscription *Subscription) {
	if subscription == nil {
		return
	}
	_ = subscription.Close()
}

// Publish routes an event to every same-tenant subscription whose authorized
// binding matches the routing key on the event's exchange. It returns the
// number of subscriptions that accepted the event into their buffers.
func (a *Authorizer) Publish(event Event) int {
	route, err := ParseTopic(event.RoutingKey)
	if err != nil {
		return 0
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	snapshot := a.state.Load()
	delivered := 0
	for subscription := range a.subs {
		if subscription.tenant != event.Tenant ||
			subscription.exchange != event.Exchange ||
			!subscription.pattern.matchesSegments(route) {
			continue
		}
		if err := snapshot.authorize(subscription.tenant, subscription.exchange, subscription.pattern); err != nil {
			subscription.revoke()
			delete(a.subs, subscription)
			continue
		}
		subscription.setGeneration(snapshot.generation)
		if subscription.enqueue(event) {
			delivered++
		}
	}
	return delivered
}

// PublishForTenant is a convenience wrapper around Publish.
func (a *Authorizer) PublishForTenant(tenant, exchange, routingKey string, payload any) int {
	return a.Publish(Event{
		Tenant:     tenant,
		Exchange:   exchange,
		RoutingKey: routingKey,
		Payload:    payload,
	})
}

// Dispatch is an alias for PublishForTenant.
func (a *Authorizer) Dispatch(tenant, exchange, routingKey string, payload any) int {
	return a.PublishForTenant(tenant, exchange, routingKey, payload)
}

// Tenant returns the subscription's tenant.
func (s *Subscription) Tenant() string {
	return s.tenant
}

// Exchange returns the subscription's exchange.
func (s *Subscription) Exchange() string {
	return s.exchange
}

// Binding returns the binding key supplied at subscription time.
func (s *Subscription) Binding() string {
	return s.binding
}

// BindingKey is an alias for Binding.
func (s *Subscription) BindingKey() string {
	return s.binding
}

// Generation returns the ACL generation observed when the subscription was
// last successfully validated.
func (s *Subscription) Generation() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.generation
}

// Dropped returns the number of events discarded because the subscription
// buffer was full.
func (s *Subscription) Dropped() uint64 {
	return atomic.LoadUint64(&s.dropped)
}

// Revoked reports whether an ACL change or delivery-time recheck revoked the
// subscription.
func (s *Subscription) Revoked() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revoked
}

// Closed reports whether the subscription was closed locally or revoked.
func (s *Subscription) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed || s.revoked
}

// Events returns the event channel. Events are drained and no further events
// are enqueued after the subscription is revoked.
func (s *Subscription) Events() <-chan Event {
	return s.events
}

// Done is closed when the subscription is closed or revoked.
func (s *Subscription) Done() <-chan struct{} {
	return s.done
}

// Receive waits for one authorized event. The ACL is checked again when the
// event is consumed, so an event queued before a revoking ACL update is never
// returned after that update.
func (s *Subscription) Receive(ctx context.Context) (Event, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	for {
		select {
		case <-ctx.Done():
			return Event{}, ctx.Err()
		case <-s.done:
			return Event{}, ErrSubscriptionClosed
		case event := <-s.events:
			if err := s.verify(event); err != nil {
				return Event{}, err
			}
			return event, nil
		}
	}
}

// TryReceive returns one queued authorized event without blocking.
func (s *Subscription) TryReceive() (Event, bool, error) {
	for {
		select {
		case <-s.done:
			return Event{}, false, ErrSubscriptionClosed
		case event := <-s.events:
			if err := s.verify(event); err != nil {
				return Event{}, false, err
			}
			return event, true, nil
		default:
			return Event{}, false, nil
		}
	}
}

// Close closes the subscription and removes it from its authorizer.
func (s *Subscription) Close() error {
	s.mu.Lock()
	s.closed = true
	s.drainLocked()
	s.signalDoneLocked()
	s.mu.Unlock()

	s.authorizer.removeSubscription(s)
	return nil
}

func (s *Subscription) verify(event Event) error {
	if event.Tenant != s.tenant || event.Exchange != s.exchange {
		err := deny(ErrDenied, "event belongs to another tenant or exchange")
		s.revoke()
		s.authorizer.removeSubscription(s)
		return fmt.Errorf("%w: %w", ErrSubscriptionClosed, err)
	}
	if !s.pattern.Matches(event.RoutingKey) {
		err := deny(ErrPattern, "event routing key does not match binding")
		s.revoke()
		s.authorizer.removeSubscription(s)
		return fmt.Errorf("%w: %w", ErrSubscriptionClosed, err)
	}

	snapshot := s.authorizer.state.Load()
	if err := snapshot.authorize(s.tenant, s.exchange, s.pattern); err != nil {
		s.revoke()
		s.authorizer.removeSubscription(s)
		return fmt.Errorf("%w: %w", ErrSubscriptionClosed, err)
	}
	s.setGeneration(snapshot.generation)
	return nil
}

func (s *Subscription) enqueue(event Event) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.revoked {
		return false
	}

	select {
	case s.events <- event:
		return true
	default:
		atomic.AddUint64(&s.dropped, 1)
		return false
	}
}

func (s *Subscription) setGeneration(generation uint64) {
	s.mu.Lock()
	s.generation = generation
	s.mu.Unlock()
}

func (s *Subscription) revoke() {
	s.mu.Lock()
	if !s.revoked {
		s.revoked = true
		s.drainLocked()
		s.signalDoneLocked()
	}
	s.mu.Unlock()
}

func (s *Subscription) drainLocked() {
	for {
		select {
		case <-s.events:
		default:
			return
		}
	}
}

func (s *Subscription) signalDoneLocked() {
	s.doneOnce.Do(func() {
		close(s.done)
	})
}
