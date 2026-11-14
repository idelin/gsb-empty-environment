package netsec

// InboundEvent is the raw, untrusted envelope delivered to the function
// endpoint. Every field is attacker-controlled until the [Source] verifies
// the signature; application code must never inspect this type directly.
type InboundEvent struct {
	// TriggerID identifies the registered trigger that claims to have
	// produced the event.
	TriggerID string `json:"triggerId"`

	// FunctionID is the function the event claims to target.
	FunctionID string `json:"functionId"`

	// Topic is the claimed event topic.
	Topic string `json:"topic"`

	// Queue is the claimed source queue.
	Queue string `json:"queue"`

	// IdempotencyKey uniquely identifies this delivery for deduplication.
	IdempotencyKey string `json:"idempotencyKey"`

	// Object is the raw payload the function would process.
	Object []byte `json:"object"`

	// Signature is the raw HMAC-SHA256 over the canonical event. It is set
	// by the trusted event bus and transported out-of-band on HTTP.
	Signature []byte `json:"-"`
}

// VerifiedEvent is the trusted material handed to a handler after the [Source]
// has proven the envelope originated from a registered trigger. Fields here
// are safe to act on.
type VerifiedEvent struct {
	TriggerID      string
	FunctionID     string
	Topic          string
	Queue          string
	IdempotencyKey string
	Object         []byte
}

func (e InboundEvent) verified() VerifiedEvent {
	return VerifiedEvent{
		TriggerID:      e.TriggerID,
		FunctionID:     e.FunctionID,
		Topic:          e.Topic,
		Queue:          e.Queue,
		IdempotencyKey: e.IdempotencyKey,
		Object:         append([]byte(nil), e.Object...),
	}
}
