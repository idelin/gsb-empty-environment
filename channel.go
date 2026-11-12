package netsec

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

type consumerState struct {
	tag   string
	queue Resource
}

// Consumer is an active consumer registration on a channel.
type Consumer struct {
	Tag   string
	Queue Resource
}

// Delivery is a message handed to an active consumer. Tag is unique among
// outstanding deliveries on the current channel instance, but not across
// channel instances that reuse the same channel number.
type Delivery struct {
	ChannelNumber uint16
	ChannelEpoch  uint64
	ConsumerTag   string
	Tag           uint64
	Body          []byte
}

// Channel is one open AMQP channel instance. Channel numbers can be reused, but
// a Channel value always represents exactly one epoch and cannot be revived
// after Close.
type Channel struct {
	conn   *Connection
	number uint16
	epoch  uint64

	mu              sync.Mutex
	closed          bool
	cache           map[permissionKey]permissionEntry
	consumers       map[string]*consumerState
	deliveries      map[uint64]*Delivery
	nextDeliveryTag uint64
}

// Number returns the AMQP channel number.
func (ch *Channel) Number() uint16 {
	return ch.number
}

// Epoch returns the instance generation for this channel number. It increases
// when a closed channel number is reopened.
func (ch *Channel) Epoch() uint64 {
	return ch.epoch
}

// IsOpen reports whether this channel instance is still open.
func (ch *Channel) IsOpen() bool {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	return !ch.closed
}

// Check authorizes one configure, write, or read operation against resource.
// The decision is cached on this channel together with the current credential
// generation. An entry from another generation is never used.
func (ch *Channel) Check(access Access, resource Resource) error {
	_, err := ch.conn.authorize(ch, access, resource)
	return err
}

// Configure performs a configure authorization check.
func (ch *Channel) Configure(resource Resource) error {
	return ch.Check(Configure, resource)
}

// Write performs a write authorization check.
func (ch *Channel) Write(resource Resource) error {
	return ch.Check(Write, resource)
}

// Read performs a read authorization check.
func (ch *Channel) Read(resource Resource) error {
	return ch.Check(Read, resource)
}

// DeclareQueue applies the configure permission to a queue declaration.
func (ch *Channel) DeclareQueue(name string) error {
	return ch.Configure(Queue(name))
}

// DeleteQueue applies the configure permission to a queue deletion.
func (ch *Channel) DeleteQueue(name string) error {
	return ch.Configure(Queue(name))
}

// Publish applies the write permission to the destination exchange.
func (ch *Channel) Publish(exchange string) error {
	return ch.Write(Exchange(exchange))
}

// CachedPermission reports the cached decision for access/resource and the
// credential generation that produced it. ok is false if no decision is cached
// for this live channel instance.
func (ch *Channel) CachedPermission(access Access, resource Resource) (allowed bool, generation uint64, ok bool) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.closed {
		return false, 0, false
	}
	entry, ok := ch.cache[permissionKey{access: access, resource: resource}]
	if !ok {
		return false, 0, false
	}
	return entry.allowed, entry.generation, true
}

// Consume registers a consumer after a read authorization check. The consumer
// is checked against the credential generation that is current at registration;
// it is checked again before every delivery.
func (ch *Channel) Consume(queue, tag string) (Consumer, error) {
	if tag == "" {
		return Consumer{}, fmt.Errorf("%w: empty consumer tag", ErrUnknownConsumer)
	}

	resource := Queue(queue)
	if err := ch.Read(resource); err != nil {
		return Consumer{}, err
	}

	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.closed {
		return Consumer{}, ErrChannelClosed
	}
	if _, exists := ch.consumers[tag]; exists {
		return Consumer{}, fmt.Errorf("%w: %q", ErrConsumerExists, tag)
	}
	state := &consumerState{tag: tag, queue: resource}
	ch.consumers[tag] = state
	return Consumer{Tag: tag, Queue: resource}, nil
}

// CancelConsumer removes a consumer without affecting its already outstanding
// deliveries. It returns ErrUnknownConsumer if tag is not active.
func (ch *Channel) CancelConsumer(tag string) error {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.closed {
		return ErrChannelClosed
	}
	if _, exists := ch.consumers[tag]; !exists {
		return fmt.Errorf("%w: %q", ErrUnknownConsumer, tag)
	}
	delete(ch.consumers, tag)
	return nil
}

// Consumers returns a snapshot of the active consumers in tag order.
func (ch *Channel) Consumers() []Consumer {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.closed {
		return nil
	}
	consumers := make([]Consumer, 0, len(ch.consumers))
	for _, state := range ch.consumers {
		consumers = append(consumers, Consumer{Tag: state.tag, Queue: state.queue})
	}
	sort.Slice(consumers, func(i, j int) bool {
		return consumers[i].Tag < consumers[j].Tag
	})
	return consumers
}

// Deliver re-authorizes the consumer's read permission immediately before
// handing over a message. A failed check removes the consumer and returns an
// error matching both ErrConsumerCancelled and ErrAccessRefused. No delivery
// tag is allocated in that case.
//
// The body is copied so the caller may reuse its buffer after Deliver returns.
func (ch *Channel) Deliver(consumerTag string, body []byte) (*Delivery, error) {
	ch.mu.Lock()
	if ch.closed {
		ch.mu.Unlock()
		return nil, ErrChannelClosed
	}
	consumer, ok := ch.consumers[consumerTag]
	ch.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownConsumer, consumerTag)
	}

	for {
		generation, err := ch.conn.authorize(ch, Read, consumer.queue)
		if err != nil {
			if errors.Is(err, ErrAccessRefused) {
				ch.conn.authMu.RLock()
				if ch.conn.Generation() != generation {
					ch.conn.authMu.RUnlock()
					continue
				}
				ch.cancelIfSame(consumerTag, consumer)
				ch.conn.authMu.RUnlock()
				return nil, fmt.Errorf("%w: consumer %q: %w", ErrConsumerCancelled, consumerTag, err)
			}
			return nil, err
		}

		// Holding authMu for the tag allocation makes "authorized" and
		// "delivery tag issued" one step with respect to UpdateSecret. A
		// refresh either happens before this check (and forces another
		// authorization round) or after the delivery has been committed.
		ch.conn.authMu.RLock()
		if ch.conn.Generation() != generation {
			ch.conn.authMu.RUnlock()
			continue
		}

		ch.mu.Lock()
		if ch.closed {
			ch.mu.Unlock()
			ch.conn.authMu.RUnlock()
			return nil, ErrChannelClosed
		}
		if current, exists := ch.consumers[consumerTag]; !exists || current != consumer {
			ch.mu.Unlock()
			ch.conn.authMu.RUnlock()
			return nil, fmt.Errorf("%w: %q", ErrUnknownConsumer, consumerTag)
		}

		tag := ch.nextDeliveryTag
		ch.nextDeliveryTag++
		delivery := &Delivery{
			ChannelNumber: ch.number,
			ChannelEpoch:  ch.epoch,
			ConsumerTag:   consumerTag,
			Tag:           tag,
			Body:          append([]byte(nil), body...),
		}
		ch.deliveries[tag] = delivery
		ch.mu.Unlock()
		ch.conn.authMu.RUnlock()
		return delivery, nil
	}
}

func (ch *Channel) cancelIfSame(tag string, consumer *consumerState) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if current, exists := ch.consumers[tag]; exists && current == consumer {
		delete(ch.consumers, tag)
	}
}

// Ack removes an outstanding delivery on this channel instance. A closed
// instance, or a tag that was never issued or was issued by an earlier
// instance of the same channel number, returns ErrUnknownDeliveryTag.
func (ch *Channel) Ack(tag uint64) error {
	return ch.settle(tag)
}

// Nack removes an outstanding delivery on this channel instance. The requeue
// argument is advisory to the broker and does not change tag validation.
func (ch *Channel) Nack(tag uint64, requeue bool) error {
	_ = requeue
	return ch.settle(tag)
}

func (ch *Channel) settle(tag uint64) error {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.closed {
		return fmt.Errorf("%w: %w: channel %d tag %d", ErrChannelClosed, ErrUnknownDeliveryTag, ch.number, tag)
	}
	if _, exists := ch.deliveries[tag]; !exists {
		return fmt.Errorf("%w: channel %d tag %d", ErrUnknownDeliveryTag, ch.number, tag)
	}
	delete(ch.deliveries, tag)
	return nil
}

// OutstandingDeliveryTags returns the unsettled delivery tags of this channel
// instance in ascending order.
func (ch *Channel) OutstandingDeliveryTags() []uint64 {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.closed {
		return nil
	}
	tags := make([]uint64, 0, len(ch.deliveries))
	for tag := range ch.deliveries {
		tags = append(tags, tag)
	}
	sort.Slice(tags, func(i, j int) bool { return tags[i] < tags[j] })
	return tags
}

// Close closes this channel instance, removes it from its connection, and
// discards its consumers, permission cache, outstanding deliveries, and
// delivery-tag counter. A later OpenChannel with the same number starts a new
// instance at delivery tag one. Close is idempotent.
func (ch *Channel) Close() error {
	ch.mu.Lock()
	if ch.closed {
		ch.mu.Unlock()
		return nil
	}
	ch.closed = true
	ch.cache = nil
	ch.consumers = nil
	ch.deliveries = nil
	ch.nextDeliveryTag = 1
	ch.mu.Unlock()

	ch.conn.mu.Lock()
	if current := ch.conn.channels[ch.number]; current == ch {
		delete(ch.conn.channels, ch.number)
	}
	ch.conn.mu.Unlock()
	return nil
}
