package netsec

import (
	"fmt"
	"sync"
	"sync/atomic"
)

type credentialState struct {
	secret     string
	generation uint64
}

// Connection owns the credential generation and the open channels of one AMQP
// connection. The zero value is not usable; call NewConnection.
type Connection struct {
	id          string
	authorizer  Authorizer
	credentials atomic.Pointer[credentialState]

	authMu   sync.RWMutex
	mu       sync.RWMutex
	channels map[uint16]*Channel
	epochs   map[uint16]uint64
}

// NewConnection creates a connection with initialSecret at generation one.
// A nil authorizer denies every operation.
func NewConnection(id, initialSecret string, authorizer Authorizer) *Connection {
	c := &Connection{
		id:         id,
		authorizer: authorizer,
		channels:   make(map[uint16]*Channel),
		epochs:     make(map[uint16]uint64),
	}
	c.credentials.Store(&credentialState{
		secret:     initialSecret,
		generation: 1,
	})
	return c
}

// ID returns the connection identifier supplied to NewConnection.
func (c *Connection) ID() string {
	return c.id
}

// Generation returns the current credential generation.
func (c *Connection) Generation() uint64 {
	return c.credentials.Load().generation
}

// UpdateSecret installs a new secret and advances the credential generation.
// Every channel cache entry from the previous generation becomes unusable; the
// next authorization check on any channel must call the Authorizer again before
// it can succeed. The new generation is returned.
//
// UpdateSecret is safe to call concurrently with channel operations. If a check
// races with the refresh, the check retries instead of storing a decision made
// under the superseded generation.
func (c *Connection) UpdateSecret(secret string) uint64 {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	old := c.credentials.Load()
	next := &credentialState{
		secret:     secret,
		generation: old.generation + 1,
	}
	c.credentials.Store(next)
	return next.generation
}

// OpenChannel opens a channel with the AMQP channel number. A number may be
// reused after its previous channel instance is closed, but an already open
// number cannot be opened twice. Each open creates a fresh epoch, consumer set,
// permission cache, and delivery-tag space.
func (c *Connection) OpenChannel(number uint16) (*Channel, error) {
	if number == 0 {
		return nil, ErrInvalidChannel
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.channels[number]; exists {
		return nil, fmt.Errorf("%w: %d", ErrChannelInUse, number)
	}
	c.epochs[number]++
	ch := &Channel{
		conn:            c,
		number:          number,
		epoch:           c.epochs[number],
		cache:           make(map[permissionKey]permissionEntry),
		consumers:       make(map[string]*consumerState),
		deliveries:      make(map[uint64]*Delivery),
		nextDeliveryTag: 1,
	}
	c.channels[number] = ch
	return ch, nil
}

// Channel returns the currently open channel instance for number.
func (c *Connection) Channel(number uint16) (*Channel, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ch, ok := c.channels[number]
	return ch, ok
}

// Ack settles an outstanding delivery on the currently open channel number.
// A missing channel or a tag from a closed channel instance is reported as
// ErrUnknownDeliveryTag.
func (c *Connection) Ack(number uint16, tag uint64) error {
	ch, ok := c.Channel(number)
	if !ok {
		return fmt.Errorf("%w: %w: channel %d tag %d", ErrUnknownDeliveryTag, ErrUnknownChannel, number, tag)
	}
	return ch.Ack(tag)
}

// Nack settles an outstanding delivery on the currently open channel number.
// The requeue decision belongs to the broker; this package only validates and
// removes the delivery-tag binding. A stale tag is reported as
// ErrUnknownDeliveryTag.
func (c *Connection) Nack(number uint16, tag uint64, requeue bool) error {
	ch, ok := c.Channel(number)
	if !ok {
		return fmt.Errorf("%w: %w: channel %d tag %d", ErrUnknownDeliveryTag, ErrUnknownChannel, number, tag)
	}
	return ch.Nack(tag, requeue)
}

type permissionKey struct {
	access   Access
	resource Resource
}

type permissionEntry struct {
	generation uint64
	allowed    bool
}

// authorize resolves a channel permission against the current credential
// generation. A cache hit is valid only for the exact generation that produced
// it. A result computed while a refresh raced the check is discarded and the
// check is retried under the new generation.
func (c *Connection) authorize(ch *Channel, access Access, resource Resource) (uint64, error) {
	if access == 0 || access&^(Configure|Write|Read) != 0 {
		return 0, fmt.Errorf("%w: %s", ErrInvalidAccess, access)
	}

	key := permissionKey{access: access, resource: resource}
	for {
		snapshot := c.credentials.Load()

		ch.mu.Lock()
		if ch.closed {
			ch.mu.Unlock()
			return 0, ErrChannelClosed
		}
		entry, ok := ch.cache[key]
		ch.mu.Unlock()

		if ok && entry.generation == snapshot.generation {
			if entry.allowed {
				return entry.generation, nil
			}
			return entry.generation, accessRefused(access, resource, entry.generation)
		}

		credential := Credential{
			ConnectionID: c.id,
			Generation:   snapshot.generation,
			Secret:       snapshot.secret,
		}
		allowed := false
		if c.authorizer != nil {
			allowed = c.authorizer.Authorize(credential, resource, access)
		}

		if c.credentials.Load().generation != snapshot.generation {
			continue
		}

		ch.mu.Lock()
		if ch.closed {
			ch.mu.Unlock()
			return 0, ErrChannelClosed
		}
		ch.cache[key] = permissionEntry{
			generation: snapshot.generation,
			allowed:    allowed,
		}
		ch.mu.Unlock()

		if allowed {
			return snapshot.generation, nil
		}
		return snapshot.generation, accessRefused(access, resource, snapshot.generation)
	}
}

func accessRefused(access Access, resource Resource, generation uint64) error {
	return fmt.Errorf("%w: %s on %s at credential generation %d",
		ErrAccessRefused, access, resource, generation)
}
