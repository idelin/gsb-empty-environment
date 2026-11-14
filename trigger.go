package netsec

import "sync"

// Trigger describes a trusted event source registered with the platform. The
// event bus holds the SigningKey and uses it to sign every event emitted for
// this trigger; function code only uses the key to verify.
type Trigger struct {
	// ID is the unique, platform-assigned trigger identifier.
	ID string

	// FunctionID is the function this trigger is permitted to invoke.
	FunctionID string

	// Topic is the only event topic this trigger may deliver.
	Topic string

	// Queue is the only queue this trigger may deliver events from.
	Queue string

	// SigningKey is the shared HMAC secret held by the trusted event bus.
	SigningKey []byte
}

func (t Trigger) valid() bool {
	return t.ID != "" && t.FunctionID != "" && t.Topic != "" && t.Queue != "" && len(t.SigningKey) > 0
}

// Registry is the set of triggers the function is willing to accept events
// from. It is safe for concurrent use.
type Registry struct {
	mu       sync.RWMutex
	triggers map[string]Trigger
}

// NewRegistry creates an empty trigger registry.
func NewRegistry() *Registry {
	return &Registry{triggers: make(map[string]Trigger)}
}

// Register adds a trigger. It returns ErrInvalidTrigger for incomplete
// definitions and ErrTriggerAlreadyRegistered if the ID is taken.
func (r *Registry) Register(t Trigger) error {
	if !t.valid() {
		return ErrInvalidTrigger
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.triggers[t.ID]; exists {
		return ErrTriggerAlreadyRegistered
	}
	cp := append([]byte(nil), t.SigningKey...)
	t.SigningKey = cp
	r.triggers[t.ID] = t
	return nil
}

// Lookup returns the trigger registered under id.
func (r *Registry) Lookup(id string) (Trigger, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.triggers[id]
	return t, ok
}
