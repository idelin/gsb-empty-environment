package applog

import "maps"

// RegisterConsumer adds a consumer whose ack position starts at start
// (0 consumes from the beginning of the log). The registration is durable
// before it returns: a registered consumer pins retention until it is
// removed.
func (l *Log) RegisterConsumer(id string, start uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return ErrClosed
	}
	if _, ok := l.consumers[id]; ok {
		return ErrConsumerExists
	}
	if start > l.nextOffset {
		return ErrAckBeyondHighWater
	}
	l.consumers[id] = start
	if err := l.persistManifestLocked(); err != nil {
		delete(l.consumers, id)
		return err
	}
	return nil
}

// RemoveConsumer unregisters a consumer. Its pin on the retention watermark
// is released as soon as the removal is durable, so the watermark may advance
// on the next Reclaim.
func (l *Log) RemoveConsumer(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return ErrClosed
	}
	pos, ok := l.consumers[id]
	if !ok {
		return ErrUnknownConsumer
	}
	delete(l.consumers, id)
	if err := l.persistManifestLocked(); err != nil {
		l.consumers[id] = pos
		return err
	}
	return nil
}

// Ack advances consumer id's position to pos, meaning "every record with
// offset < pos has been consumed".
//
// Contract:
//   - pos must not exceed the high watermark (NextOffset): a consumer cannot
//     acknowledge records it cannot have read.
//   - pos must not move the position backwards (ErrNonMonotonicAck).
//   - Ack of the current position is an idempotent no-op returning nil, so
//     retrying an ack whose response was lost after it was committed never
//     rolls the position back.
//
// The new position is durable (manifest fsynced + renamed) before Ack
// returns nil.
func (l *Log) Ack(id string, pos uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return ErrClosed
	}
	cur, ok := l.consumers[id]
	if !ok {
		return ErrUnknownConsumer
	}
	if pos > l.nextOffset {
		return ErrAckBeyondHighWater
	}
	if pos < cur {
		return ErrNonMonotonicAck
	}
	if pos == cur {
		return nil // idempotent retry of an already-committed ack
	}
	l.consumers[id] = pos
	if err := l.persistManifestLocked(); err != nil {
		l.consumers[id] = cur
		return err
	}
	return nil
}

// AckBatch applies several cursor updates atomically: every entry is
// validated against the pre-batch state, then a single manifest commit
// persists all of them. If any entry is invalid or the commit fails, no
// position changes (all-or-nothing).
func (l *Log) AckBatch(acks map[string]uint64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return ErrClosed
	}
	for id, pos := range acks {
		cur, ok := l.consumers[id]
		if !ok {
			return ErrUnknownConsumer
		}
		if pos > l.nextOffset {
			return ErrAckBeyondHighWater
		}
		if pos < cur {
			return ErrNonMonotonicAck
		}
	}
	prev := maps.Clone(l.consumers)
	maps.Copy(l.consumers, acks)
	if err := l.persistManifestLocked(); err != nil {
		l.consumers = prev
		return err
	}
	return nil
}

// Position returns the current ack position of a consumer.
func (l *Log) Position(id string) (uint64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	pos, ok := l.consumers[id]
	if !ok {
		return 0, ErrUnknownConsumer
	}
	return pos, nil
}

// Consumers returns a snapshot of all consumer positions.
func (l *Log) Consumers() map[string]uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return maps.Clone(l.consumers)
}

// Watermark returns the retention watermark: every record with offset below
// it has been consumed by all registered consumers and is eligible for
// reclamation. With no registered consumers nothing pins retention, so the
// watermark is the high watermark itself.
func (l *Log) Watermark() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.watermarkLocked()
}

func (l *Log) watermarkLocked() uint64 {
	if len(l.consumers) == 0 {
		return l.nextOffset
	}
	min := l.nextOffset
	for _, pos := range l.consumers {
		if pos < min {
			min = pos
		}
	}
	return min
}
