// Package router binds extension port capabilities to the lifetime of a
// browser document.
//
// A browser extension talks to a Go native messaging host over a single
// native-messaging connection and multiplexes logical "ports" over it.
// Messages arriving on that connection cannot be trusted to name their own
// origin: the host learns the origin from trusted connection metadata
// supplied by the browser at handshake time. This package keeps every
// capability grant scoped to the exact document that performed the handshake
// and revokes the whole grant when the document navigates away or closes.
//
// Tab identifiers are reused by browsers, so state is keyed by the
// (tabID, documentID) pair rather than by tab id alone. Every document also
// carries a monotonically increasing generation, and every port records the
// generation it was opened under. Late asynchronous callbacks therefore
// cannot leak into a replacement document: they hold either a closed *Port
// or a Lease whose generation no longer matches, and the router drops the
// delivery.
package router

import (
	"errors"
	"sync"
)

// Capability is an authorization token granted to a port for the lifetime
// of its document. Values are opaque to the router; the configured Policy
// decides which ones any given origin may hold.
type Capability string

// Standard errors returned by the router. Callers should prefer errors.Is
// over string matching.
var (
	// ErrRejected is returned when a handshake asks for a capability that
	// the policy does not grant to the connecting origin.
	ErrRejected = errors.New("router: capability rejected by policy")

	// ErrUnknownPort is returned when an operation references a port id
	// that the router has no record of, including one already revoked.
	// Out-of-order disconnects surface as this error rather than
	// corrupting newer state.
	ErrUnknownPort = errors.New("router: unknown port")

	// ErrPortClosed is returned when a delivery is attempted on a port
	// whose document has navigated away or whose connection was torn down.
	ErrPortClosed = errors.New("router: port closed")

	// ErrOriginMismatch is returned when the origin asserted inside a
	// handshake payload does not match the origin carried by the trusted
	// connection metadata. The trusted metadata always wins.
	ErrOriginMismatch = errors.New("router: origin does not match trusted connection metadata")

	// ErrBadHandshake is returned for malformed handshake metadata.
	ErrBadHandshake = errors.New("router: invalid handshake metadata")
)

// TrustedMeta is the connection metadata established by the browser outside
// the wire payload. Its fields are the only origin inputs the router trusts;
// an origin field travelling inside an extension message is treated as a
// claim that must match this value.
type TrustedMeta struct {
	// TabID is the browser tab the connecting document lives in.
	TabID int64

	// DocumentID uniquely identifies the loaded document instance. It
	// changes on every navigation, including same-tab reloads.
	DocumentID string

	// Origin is the web origin (scheme://host[:port]) of the document.
	Origin string
}

// DocKey identifies a document instance. DocumentID changes on navigation,
// so a reused tab id produces a fresh key and never inherits prior grants.
type DocKey struct {
	TabID      int64
	DocumentID string
}

// HandshakeRequest is the logical content of an extension handshake. The
// ClaimedOrigin field comes from the untrusted message body and must equal
// TrustedMeta.Origin; it exists purely as a tamper check.
type HandshakeRequest struct {
	// ClaimedOrigin is the origin asserted inside the message payload.
	ClaimedOrigin string

	// PortID is the logical port the extension wants to open. It must be
	// unique among live ports.
	PortID string

	// Capabilities are the grants the port asks for. Every entry must be
	// allowed for the trusted origin or the whole handshake is rejected.
	Capabilities []Capability
}

// Policy decides which capabilities an origin may hold. Implementations
// must be safe for concurrent use.
type Policy interface {
	// Allow reports whether origin may hold cap.
	Allow(origin string, cap Capability) bool
}

// StaticPolicy is a Policy backed by an explicit per-origin allowlist.
type StaticPolicy struct {
	mu    sync.RWMutex
	allow map[string]map[Capability]struct{}
}

// NewStaticPolicy builds a StaticPolicy from a map of origin to the set of
// capabilities that origin may request.
func NewStaticPolicy(entries map[string][]Capability) *StaticPolicy {
	allow := make(map[string]map[Capability]struct{}, len(entries))
	for origin, caps := range entries {
		set := make(map[Capability]struct{}, len(caps))
		for _, cap := range caps {
			set[cap] = struct{}{}
		}
		allow[origin] = set
	}
	return &StaticPolicy{allow: allow}
}

// Allow reports whether cap is on origin's allowlist.
func (p *StaticPolicy) Allow(origin string, cap Capability) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	set, ok := p.allow[origin]
	if !ok {
		return false
	}
	_, ok = set[cap]
	return ok
}

// Sink receives messages that a port delivers. It is the boundary toward the
// underlying native-messaging transport, kept behind an interface so tests
// can record deliveries without standing up a browser. Implementations must
// be safe for concurrent use.
type Sink interface {
	// Send delivers payload to the extension port identified by portID.
	Send(portID string, payload any)
}

// discardSink is the default Sink used when none is supplied.
type discardSink struct{}

func (discardSink) Send(string, any) {}

// document holds every port opened against one loaded document instance.
type document struct {
	key    DocKey
	origin string
	epoch  uint64
	ports  map[string]*Port
	closed bool
	caps   map[Capability]struct{}
}

// Port is one logical multiplexed connection from a document. A Port becomes
// unusable the moment its document navigates or closes.
type Port struct {
	router *Router
	id     string
	doc    DocKey
	epoch  uint64
	closed bool

	// inflight counts Deliver calls that have passed the liveness check
	// but have not yet returned. Close waits for them to drain so no
	// goroutine can touch the Sink after revocation has completed.
	inflight sync.WaitGroup
}

// ID returns the port identifier supplied at handshake.
func (p *Port) ID() string { return p.id }

// Document returns the document instance the port is bound to.
func (p *Port) Document() DocKey { return p.doc }

// Origin returns the trusted origin the port was granted for.
func (p *Port) Origin() string { return p.router.originOf(p) }

// Capabilities returns the capabilities granted to the port's document.
func (p *Port) Capabilities() []Capability { return p.router.capsOf(p) }

// Closed reports whether the port has been revoked.
func (p *Port) Closed() bool {
	p.router.mu.RLock()
	defer p.router.mu.RUnlock()
	return p.closed
}

// Lease is a detached, copyable handle that a slow asynchronous worker may
// capture. It outlives stack references to the Port but cannot outlive the
// document: Deliver re-validates the generation before sending, so a result
// computed after navigation is dropped instead of reaching the new document.
type Lease struct {
	router *Router
	portID string
	doc    DocKey
	epoch  uint64
}

// Lease returns a detached handle bound to this port's document generation.
func (p *Port) Lease() *Lease {
	return &Lease{router: p.router, portID: p.id, doc: p.doc, epoch: p.epoch}
}

// Router multiplexes extension ports and keeps their grants tied to document
// lifetimes. The zero value is not usable; construct one with New.
type Router struct {
	mu         sync.RWMutex
	policy     Policy
	sink       Sink
	docs       map[DocKey]*document
	tabIndex   map[int64]DocKey
	ports      map[string]*Port
	generation uint64
}

// New creates a Router. policy authorizes capability requests and sink
// receives outbound messages; a nil sink silently discards deliveries.
func New(policy Policy, sink Sink) *Router {
	if sink == nil {
		sink = discardSink{}
	}
	return &Router{
		policy:   policy,
		sink:     sink,
		docs:     make(map[DocKey]*document),
		tabIndex: make(map[int64]DocKey),
		ports:    make(map[string]*Port),
	}
}

// Handshake opens a port on behalf of a document. meta is the trusted
// connection metadata supplied by the browser; req travels inside the
// extension message.
//
// If the tab already hosts a different document, the previous document and
// all of its ports are revoked first: navigation tears down every old
// capability before the new grant exists. A handshake for a document that is
// already known simply opens an additional port on it.
func (r *Router) Handshake(meta TrustedMeta, req HandshakeRequest) (*Port, error) {
	if meta.DocumentID == "" || meta.Origin == "" || req.PortID == "" {
		return nil, ErrBadHandshake
	}
	if req.ClaimedOrigin != "" && req.ClaimedOrigin != meta.Origin {
		return nil, ErrOriginMismatch
	}
	for _, cap := range req.Capabilities {
		if !r.policy.Allow(meta.Origin, cap) {
			return nil, ErrRejected
		}
	}

	r.mu.Lock()
	if _, exists := r.ports[req.PortID]; exists {
		r.mu.Unlock()
		return nil, ErrRejected
	}

	// A tab id reused after navigation points at a stale document. Revoke
	// it entirely before binding anything to the new instance.
	if prior, ok := r.tabIndex[meta.TabID]; ok && prior.DocumentID != meta.DocumentID {
		old := r.docs[prior]
		r.mu.Unlock()
		r.closeDocument(prior, old)
		r.mu.Lock()
	}

	key := DocKey{TabID: meta.TabID, DocumentID: meta.DocumentID}
	doc, ok := r.docs[key]
	if !ok {
		r.generation++
		doc = &document{
			key:    key,
			origin: meta.Origin,
			epoch:  r.generation,
			ports:  make(map[string]*Port),
			caps:   make(map[Capability]struct{}),
		}
		r.docs[key] = doc
		r.tabIndex[meta.TabID] = key
	}

	port := &Port{
		router: r,
		id:     req.PortID,
		doc:    key,
		epoch:  doc.epoch,
	}
	doc.ports[req.PortID] = port
	r.ports[req.PortID] = port
	for _, cap := range req.Capabilities {
		doc.caps[cap] = struct{}{}
	}
	r.mu.Unlock()
	return port, nil
}

// OnDisconnect handles a port disconnect event from the extension. It is
// idempotent: a duplicate or out-of-order event for an already-revoked port
// returns ErrUnknownPort and never touches a port that reused the id under
// a newer document.
func (r *Router) OnDisconnect(portID string) error {
	r.mu.Lock()
	port, ok := r.ports[portID]
	if !ok {
		r.mu.Unlock()
		return ErrUnknownPort
	}
	doc := r.docs[port.doc]
	r.removePortLocked(port, doc)
	r.mu.Unlock()

	port.inflight.Wait()
	return nil
}

// OnDocumentClose handles a document teardown event. As with
// OnDisconnect, stale events for an already-replaced document are harmless
// no-ops reported as ErrUnknownPort.
func (r *Router) OnDocumentClose(key DocKey) error {
	r.mu.Lock()
	doc, ok := r.docs[key]
	if !ok {
		r.mu.Unlock()
		return ErrUnknownPort
	}
	r.mu.Unlock()
	return r.closeDocument(key, doc)
}

// closeDocument revokes the document and every port it owns. The wait for
// in-flight deliveries happens without holding the router lock.
func (r *Router) closeDocument(key DocKey, doc *document) error {
	r.mu.Lock()
	if doc.closed {
		r.mu.Unlock()
		return ErrUnknownPort
	}
	pending := make([]*Port, 0, len(doc.ports))
	for _, port := range doc.ports {
		pending = append(pending, port)
		port.closed = true
		delete(r.ports, port.id)
	}
	doc.ports = make(map[string]*Port)
	doc.closed = true
	delete(r.docs, key)
	if current, ok := r.tabIndex[key.TabID]; ok && current == key {
		delete(r.tabIndex, key.TabID)
	}
	r.mu.Unlock()

	for _, port := range pending {
		port.inflight.Wait()
	}
	return nil
}

// removePortLocked detaches a single live port. Caller holds r.mu.
func (r *Router) removePortLocked(port *Port, doc *document) {
	port.closed = true
	delete(r.ports, port.id)
	if doc != nil {
		delete(doc.ports, port.id)
	}
}

// Deliver sends payload through a live port. It returns ErrPortClosed if the
// port was revoked before the send, which is the path that protects delayed
// callbacks after navigation.
func (p *Port) Deliver(payload any) error {
	p.router.mu.RLock()
	if p.closed {
		p.router.mu.RUnlock()
		return ErrPortClosed
	}
	doc, ok := p.router.docs[p.doc]
	if !ok || doc.closed || doc.epoch != p.epoch {
		p.router.mu.RUnlock()
		return ErrPortClosed
	}
	p.inflight.Add(1)
	p.router.mu.RUnlock()

	defer p.inflight.Done()
	p.router.sink.Send(p.id, payload)
	return nil
}

// Deliver resolves the lease against current router state and sends only if
// the same document generation still owns the port. A lease captured before
// navigation always fails this check.
func (l *Lease) Deliver(payload any) error {
	l.router.mu.RLock()
	port, ok := l.router.ports[l.portID]
	if !ok || port.closed || port.doc != l.doc || port.epoch != l.epoch {
		l.router.mu.RUnlock()
		return ErrPortClosed
	}
	port.inflight.Add(1)
	l.router.mu.RUnlock()

	defer port.inflight.Done()
	l.router.sink.Send(l.portID, payload)
	return nil
}

func (r *Router) originOf(p *Port) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if doc, ok := r.docs[p.doc]; ok {
		return doc.origin
	}
	return ""
}

func (r *Router) capsOf(p *Port) []Capability {
	r.mu.RLock()
	defer r.mu.RUnlock()
	doc, ok := r.docs[p.doc]
	if !ok {
		return nil
	}
	out := make([]Capability, 0, len(doc.caps))
	for cap := range doc.caps {
		out = append(out, cap)
	}
	return out
}

// LookupPort returns the live port with the given id, or ErrUnknownPort.
func (r *Router) LookupPort(portID string) (*Port, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	port, ok := r.ports[portID]
	if !ok {
		return nil, ErrUnknownPort
	}
	return port, nil
}

// ActiveDocuments returns the document instances currently holding grants.
func (r *Router) ActiveDocuments() []DocKey {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]DocKey, 0, len(r.docs))
	for key := range r.docs {
		out = append(out, key)
	}
	return out
}
