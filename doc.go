// Package netsec provides an importable, trusted event-source component for a
// serverless Go function runtime.
//
// Serverless functions are invoked through an HTTP endpoint that anybody can
// reach. An attacker can therefore call the endpoint directly and hand-craft
// an event envelope, forging the queue, topic, function binding or object
// fields in order to trigger operations the function should never perform on
// attacker-supplied input.
//
// This package never trusts the inbound envelope. Every event must:
//
//   - originate from a trigger registered in a [Registry];
//   - carry a valid HMAC signature computed by the trusted event bus over a
//     canonical, length-prefixed representation of every routing field and the
//     object payload;
//   - target the function the [Source] is bound to (cross-function events are
//     rejected);
//   - match the topic and queue declared on the registered trigger;
//   - present a unique idempotency key that has not been processed before.
//
// Business handlers only ever receive a [VerifiedEvent], whose fields are
// known to originate from the trusted event bus.
package netsec
