package netsec

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
)

// HTTP headers used to transport the trusted, out-of-band signature and
// routing metadata. Keeping the signature out of the JSON body makes it
// impossible for a replayed body to appear self-authenticating.
const (
	HeaderSignature = "X-Event-Signature"
	HeaderTrigger   = "X-Event-Trigger-Id"
	HeaderFunction  = "X-Event-Function-Id"
	HeaderTopic     = "X-Event-Topic"
	HeaderQueue     = "X-Event-Queue"
)

type contextKey struct{}

// Source is the importable event-source component bound to one function. It
// authenticates every inbound invocation before a handler is allowed to run.
type Source struct {
	functionID string
	registry   *Registry
	idem       IdempotencyStore
}

// NewSource builds the event source for functionID. The registry supplies the
// only triggers permitted to invoke the function and idempotencyStore
// deduplicates repeat deliveries.
func NewSource(functionID string, registry *Registry, idempotencyStore IdempotencyStore) *Source {
	return &Source{
		functionID: functionID,
		registry:   registry,
		idem:       idempotencyStore,
	}
}

// Process runs the full verification pipeline on an untrusted inbound event.
// It returns a [VerifiedEvent] only when the event is proven to come from a
// registered trigger bound to this function and has not been processed
// before. Every rejection is reported through a sentinel error.
func (s *Source) Process(e InboundEvent) (VerifiedEvent, error) {
	if e.TriggerID == "" {
		return VerifiedEvent{}, ErrMissingTriggerID
	}

	trigger, ok := s.registry.Lookup(e.TriggerID)
	if !ok {
		return VerifiedEvent{}, ErrTriggerNotRegistered
	}

	// The signature covers every attacker-controlled routing and payload
	// field, so this single check defeats field forgery and tampering.
	if len(e.Signature) == 0 {
		return VerifiedEvent{}, ErrMissingSignature
	}
	if !validSignature(trigger.SigningKey, e.Signature, e) {
		return VerifiedEvent{}, ErrInvalidSignature
	}

	// The trigger must be bound to this exact function.
	if trigger.FunctionID != s.functionID {
		return VerifiedEvent{}, ErrCrossFunction
	}

	// The signed event must also name this function, rejecting events
	// legitimately signed for a different function and replayed here.
	if e.FunctionID != s.functionID || e.FunctionID != trigger.FunctionID {
		return VerifiedEvent{}, ErrFunctionMismatch
	}

	if e.Topic != trigger.Topic {
		return VerifiedEvent{}, ErrTopicMismatch
	}
	if e.Queue != trigger.Queue {
		return VerifiedEvent{}, ErrQueueMismatch
	}

	if e.IdempotencyKey == "" {
		return VerifiedEvent{}, ErrMissingIdempotencyKey
	}

	// Atomic claim: duplicate deliveries are rejected even under
	// concurrent retries.
	fresh, err := s.idem.CheckAndStore(e.TriggerID + ":" + e.IdempotencyKey)
	if err != nil {
		return VerifiedEvent{}, err
	}
	if !fresh {
		return VerifiedEvent{}, ErrDuplicateEvent
	}

	return e.verified(), nil
}

// HTTPHandler wraps next so it runs only for authenticated events. On
// rejection the request never reaches next; an appropriate status code is
// written instead. On success the [VerifiedEvent] is available through
// [EventFromContext].
func (s *Source) HTTPHandler(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inbound, err := decodeInbound(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}

		verified, err := s.Process(inbound)
		if err != nil {
			writeError(w, statusForError(err), err)
			return
		}

		ctx := context.WithValue(r.Context(), contextKey{}, verified)
		next(w, r.WithContext(ctx))
	})
}

// EventFromContext returns the [VerifiedEvent] attached by [Source.HTTPHandler].
func EventFromContext(ctx context.Context) (VerifiedEvent, bool) {
	v, ok := ctx.Value(contextKey{}).(VerifiedEvent)
	return v, ok
}

func decodeInbound(r *http.Request) (InboundEvent, error) {
	var e InboundEvent
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		return InboundEvent{}, err
	}

	// Routing metadata and the signature arrive in trusted transport headers;
	// header values take precedence over any body fields so the envelope
	// cannot override what the bus placed out-of-band.
	if v := r.Header.Get(HeaderTrigger); v != "" {
		e.TriggerID = v
	}
	if v := r.Header.Get(HeaderFunction); v != "" {
		e.FunctionID = v
	}
	if v := r.Header.Get(HeaderTopic); v != "" {
		e.Topic = v
	}
	if v := r.Header.Get(HeaderQueue); v != "" {
		e.Queue = v
	}

	encoded := r.Header.Get(HeaderSignature)
	if encoded != "" {
		sig, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return InboundEvent{}, ErrInvalidSignature
		}
		e.Signature = sig
	}
	return e, nil
}

func statusForError(err error) int {
	switch err {
	case ErrDuplicateEvent:
		return http.StatusConflict
	case ErrMissingSignature, ErrInvalidSignature, ErrCrossFunction,
		ErrFunctionMismatch, ErrTopicMismatch, ErrQueueMismatch:
		return http.StatusForbidden
	default:
		return http.StatusBadRequest
	}
}

func writeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
