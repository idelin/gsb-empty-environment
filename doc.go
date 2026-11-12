// Package netsec provides credential-generation-aware authorization contexts
// for AMQP 0-9-1 broker channels.
//
// A Connection owns the current credential and a monotonically increasing
// credential generation. A Channel caches configure, write, and read decisions
// per resource together with the generation that produced them. Cache entries
// from an older generation are never reused, so a connection.update-secret
// refresh forces the next operation on every channel to ask the Authorizer again.
//
// Consumers are authorized when they are created and re-authorized through the
// channel's read check immediately before every delivery. If that check fails,
// the consumer is removed and no delivery tag is allocated.
//
// Delivery tags are scoped to a particular channel instance. Closing a channel
// discards its consumers and outstanding deliveries; reopening the same channel
// number creates a new instance with a new epoch and restarts delivery tags at
// one. An ack or nack for a tag that is not outstanding on the current instance
// is reported as ErrUnknownDeliveryTag.
package netsec
