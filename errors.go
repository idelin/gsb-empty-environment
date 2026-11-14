package netsec

import "errors"

// Sentinel errors returned by the verification pipeline. Callers can use
// errors.Is to distinguish the rejection reason.
var (
	// ErrMissingTriggerID indicates the inbound event did not identify a
	// trigger.
	ErrMissingTriggerID = errors.New("netsec: missing trigger id")

	// ErrTriggerNotRegistered indicates the trigger is not known to the
	// function's registry.
	ErrTriggerNotRegistered = errors.New("netsec: trigger is not registered")

	// ErrTriggerAlreadyRegistered indicates a duplicate trigger registration.
	ErrTriggerAlreadyRegistered = errors.New("netsec: trigger already registered")

	// ErrInvalidTrigger indicates a trigger definition is incomplete.
	ErrInvalidTrigger = errors.New("netsec: invalid trigger definition")

	// ErrMissingSignature indicates a direct, unsigned invocation.
	ErrMissingSignature = errors.New("netsec: missing event signature")

	// ErrInvalidSignature indicates the signature does not match the inbound
	// fields, i.e. a forged or tampered event.
	ErrInvalidSignature = errors.New("netsec: invalid event signature")

	// ErrFunctionMismatch indicates the event targets a function other than
	// the one running this [Source].
	ErrFunctionMismatch = errors.New("netsec: event function id does not match this function")

	// ErrCrossFunction indicates the registered trigger is bound to a
	// different function and may not invoke this one.
	ErrCrossFunction = errors.New("netsec: trigger is bound to another function")

	// ErrTopicMismatch indicates the event topic differs from the trigger binding.
	ErrTopicMismatch = errors.New("netsec: event topic does not match trigger binding")

	// ErrQueueMismatch indicates the event queue differs from the trigger
	// binding.
	ErrQueueMismatch = errors.New("netsec: event queue does not match trigger binding")

	// ErrMissingIdempotencyKey indicates the event carried no idempotency key.
	ErrMissingIdempotencyKey = errors.New("netsec: missing idempotency key")

	// ErrDuplicateEvent indicates the idempotency key was already processed.
	ErrDuplicateEvent = errors.New("netsec: duplicate event delivery")
)
