package netsec

import (
	"sync"
)

// Registry is the communication host: it maps endpoint names to the current
// endpoint instance and owns the lifecycle of every accepted connection.
//
// Re-registering a name replaces the instance that new handshakes bind to.
// Connections already bound to the previous instance keep that binding, so
// an old connection can never impersonate the replacement instance.
type Registry struct {
	mu        sync.RWMutex
	endpoints map[string]*Endpoint
}

// NewRegistry creates an empty endpoint registry.
func NewRegistry() *Registry {
	return &Registry{endpoints: make(map[string]*Endpoint)}
}

// Register creates a fresh endpoint instance bound to name. A previous
// instance registered under the same name, if any, is revoked: new
// handshakes can no longer bind to it, while connections already bound to
// the old instance retain their fixed identity until they close.
func (r *Registry) Register(name string) (*Endpoint, error) {
	id, err := NewInstanceID()
	if err != nil {
		return nil, err
	}
	ep := newEndpoint(name, id, r)

	r.mu.Lock()
	if old := r.endpoints[name]; old != nil {
		old.markRevoked()
	}
	r.endpoints[name] = ep
	r.mu.Unlock()
	return ep, nil
}

// Lookup returns the endpoint instance currently registered under name.
func (r *Registry) Lookup(name string) (*Endpoint, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ep := r.endpoints[name]
	if ep == nil {
		return nil, ErrEndpointUnknown
	}
	return ep, nil
}

// Revoke removes the endpoint registered under name and revokes its
// current instance. In-flight connections bound to the instance finish
// messages they already accepted, but no new message is admitted.
func (r *Registry) Revoke(name string) error {
	r.mu.Lock()
	ep := r.endpoints[name]
	if ep == nil {
		r.mu.Unlock()
		return ErrEndpointUnknown
	}
	delete(r.endpoints, name)
	ep.markRevoked()
	r.mu.Unlock()
	return nil
}

// Names returns the currently registered endpoint names in no particular
// order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.endpoints))
	for name := range r.endpoints {
		names = append(names, name)
	}
	return names
}

// Endpoint is one registered instance of a name. The zero value is not
// usable; obtain instances through Registry.Register.
type Endpoint struct {
	registry *Registry

	mu       sync.Mutex
	cond     *sync.Cond
	revokeCh chan struct{}
	name     string
	instance InstanceID
	revoked  bool
	conns    map[*Conn]struct{}
	inbox    []Message
}

func newEndpoint(name string, id InstanceID, r *Registry) *Endpoint {
	ep := &Endpoint{
		registry: r,
		name:     name,
		instance: id,
		revokeCh: make(chan struct{}),
		conns:    make(map[*Conn]struct{}),
	}
	ep.cond = sync.NewCond(&ep.mu)
	return ep
}

// Name returns the endpoint name this instance is registered under.
func (e *Endpoint) Name() string { return e.name }

// Instance returns the random identity minted for this registration. The
// value never changes for the lifetime of the instance, even if the same
// name is later re-registered to a new instance.
func (e *Endpoint) Instance() InstanceID { return e.instance }

// Revoked reports whether this instance has been revoked, either by an
// explicit Registry.Revoke call or by a same-name re-registration.
func (e *Endpoint) Revoked() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.revoked
}

// Revoke revokes this specific instance. It is a no-op when the instance
// has already been revoked. If the instance is still the current
// registration for its name, the name mapping is removed as well.
func (e *Endpoint) Revoke() {
	e.registry.mu.Lock()
	if current := e.registry.endpoints[e.name]; current == e {
		delete(e.registry.endpoints, e.name)
	}
	e.registry.mu.Unlock()

	e.markRevoked()
}

// markRevoked flips the instance to revoked, wakes message waiters and
// closes the revocation signal channel. It acquires e.mu itself and is
// safe to call while the registry lock is held.
func (e *Endpoint) markRevoked() {
	e.mu.Lock()
	if !e.revoked {
		e.revoked = true
		close(e.revokeCh)
		e.cond.Broadcast()
	}
	e.mu.Unlock()
}

// RevokedChan returns a channel that is closed the moment this instance
// is revoked, either explicitly or by a same-name re-registration.
func (e *Endpoint) RevokedChan() <-chan struct{} { return e.revokeCh }

// ActiveConns returns the connections currently bound to this instance,
// including connections whose authentication callback is still pending.
func (e *Endpoint) ActiveConns() []*Conn {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]*Conn, 0, len(e.conns))
	for conn := range e.conns {
		out = append(out, conn)
	}
	return out
}

// Messages returns a snapshot of every message already accepted by this
// instance. Messages accepted before revocation remain available after the
// instance is revoked, so received work can finish against the fixed
// instance it arrived on.
func (e *Endpoint) Messages() []Message {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Message, len(e.inbox))
	copy(out, e.inbox)
	return out
}

// Wait blocks until at least one accepted message is available or the
// instance is revoked. It returns the current message snapshot.
func (e *Endpoint) Wait() []Message {
	e.mu.Lock()
	for len(e.inbox) == 0 && !e.revoked {
		e.cond.Wait()
	}
	msgs := append([]Message(nil), e.inbox...)
	e.mu.Unlock()
	return msgs
}
