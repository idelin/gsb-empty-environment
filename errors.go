package netsec

import "errors"

var (
	// ErrInvalidChannel is returned for channel number zero.
	ErrInvalidChannel = errors.New("netsec: invalid channel number")
	// ErrChannelInUse is returned when opening a channel number that is already open.
	ErrChannelInUse = errors.New("netsec: channel number already in use")
	// ErrUnknownChannel is returned when a connection-level operation names no open channel.
	ErrUnknownChannel = errors.New("netsec: unknown channel")
	// ErrChannelClosed is returned when an operation is attempted on a closed channel instance.
	ErrChannelClosed = errors.New("netsec: channel closed")
	// ErrInvalidAccess is returned for zero or combined permission bits.
	ErrInvalidAccess = errors.New("netsec: invalid access")
	// ErrAccessRefused is returned when the current credential does not grant the operation.
	ErrAccessRefused = errors.New("netsec: access refused")
	// ErrUnknownConsumer is returned when a consumer tag is not active on the channel.
	ErrUnknownConsumer = errors.New("netsec: unknown consumer")
	// ErrConsumerExists is returned when a consumer tag is already active on the channel.
	ErrConsumerExists = errors.New("netsec: consumer already exists")
	// ErrConsumerCancelled is returned when a consumer was removed after a failed recheck.
	ErrConsumerCancelled = errors.New("netsec: consumer cancelled")
	// ErrUnknownDeliveryTag is returned when an ack or nack does not name an outstanding
	// delivery on the current channel instance.
	ErrUnknownDeliveryTag = errors.New("netsec: unknown delivery tag")
)
