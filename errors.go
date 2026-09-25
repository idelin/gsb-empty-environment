package applog

import "errors"

var (
	// ErrClosed is returned by operations on a closed Log.
	ErrClosed = errors.New("applog: log is closed")
	// ErrConsumerExists is returned when registering a duplicate consumer ID.
	ErrConsumerExists = errors.New("applog: consumer already registered")
	// ErrUnknownConsumer is returned when acking/removing an unregistered consumer.
	ErrUnknownConsumer = errors.New("applog: unknown consumer")
	// ErrNonMonotonicAck is returned when an ack would move a consumer
	// position backwards.
	ErrNonMonotonicAck = errors.New("applog: ack would move position backwards")
	// ErrAckBeyondHighWater is returned when an ack points past the appended
	// (hence readable) range.
	ErrAckBeyondHighWater = errors.New("applog: ack beyond appended range")
	// ErrOffsetReclaimed is returned when reading an offset whose segment has
	// been reclaimed.
	ErrOffsetReclaimed = errors.New("applog: offset has been reclaimed")
	// ErrOffsetOutOfRange is returned when reading an offset that was never
	// appended.
	ErrOffsetOutOfRange = errors.New("applog: offset out of range")
)
