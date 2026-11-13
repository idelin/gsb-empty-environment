package netsec

import "sync"

// VulnerablePipe is the implementation under review. It keeps the request
// and the connection binding in separate places, never ties them together:
//
//   - Impersonate ignores the snapshot captured at read time and binds
//     whatever connection is current (or the host identity when the client
//     is gone).
//   - Disconnect does not revoke an in-flight impersonation.
//   - There is no generation counter, so a reused instance and a reused
//     client PID are indistinguishable from the original connection.
type VulnerablePipe struct {
	mu         sync.Mutex
	instanceID uint64
	state      State
	conn       ConnectionSnapshot
	hasConn    bool
	inbound    []byte

	impersonating bool
	impersonated  Identity
}

// NewVulnerableInstance creates a reviewed-instance in the Listening state.
func (h *Host) NewVulnerableInstance() *VulnerablePipe {
	h.mu.Lock()
	h.nextInstanceID++
	id := h.nextInstanceID
	h.mu.Unlock()
	return &VulnerablePipe{instanceID: id, state: StateListening}
}

func (p *VulnerablePipe) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

func (p *VulnerablePipe) Listen() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != StateDisconnected {
		return errProtocolState
	}
	p.state = StateListening
	return nil
}

func (p *VulnerablePipe) Connect(id Identity) (ConnectionSnapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != StateListening {
		return ConnectionSnapshot{}, errProtocolState
	}
	p.conn = ConnectionSnapshot{InstanceID: p.instanceID, Identity: id}
	p.hasConn = true
	p.state = StateConnected
	return p.conn, nil
}

func (p *VulnerablePipe) WriteFromClient(chunk []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != StateConnected && p.state != StateReading {
		return errProtocolState
	}
	p.inbound = append(p.inbound, chunk...)
	return nil
}

func (p *VulnerablePipe) ReadRequest() (*Request, ConnectionSnapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != StateConnected {
		return nil, ConnectionSnapshot{}, errProtocolState
	}
	id, payload, ok := decodeFrame(p.inbound)
	if !ok {
		return nil, ConnectionSnapshot{}, errIncompleteFrame
	}
	p.inbound = nil
	p.state = StateReading
	return &Request{id: id, data: payload}, p.conn, nil
}

// Impersonate binds the *current* connection (or falls back to the host
// process identity) instead of the connection that delivered the request.
func (p *VulnerablePipe) Impersonate(host Identity) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != StateConnected && p.state != StateReading {
		// Client already gone: silently fall back to the process identity.
		p.impersonated = host
		p.impersonating = true
		return nil
	}
	p.impersonated = p.conn.Identity
	p.impersonating = true
	p.state = StateImpersonating
	return nil
}

// Call performs the sensitive operation without re-validating anything.
func (p *VulnerablePipe) Call() Identity {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.impersonated
}

func (p *VulnerablePipe) Revert() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.impersonating = false
	if p.state == StateImpersonating {
		p.state = StateConnected
	}
}

// Disconnect drops the connection but leaves any in-flight impersonation
// and any already-read request untouched.
func (p *VulnerablePipe) Disconnect() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.hasConn = false
	p.conn = ConnectionSnapshot{}
	p.inbound = nil
	if p.state == StateConnected || p.state == StateReading {
		p.state = StateDisconnected
	}
}

// VulnerableService is the reviewed service built on VulnerablePipe.
type VulnerableService struct {
	host *Host
}

func NewVulnerableService(h *Host) *VulnerableService {
	return &VulnerableService{host: h}
}

// VulnerableTransaction mirrors Transaction so the same controllable-client
// driver and trace search can step both implementations.
type VulnerableTransaction struct {
	svc          *VulnerableService
	pipe         *VulnerablePipe
	req          *Request
	impersonated bool
}

func (s *VulnerableService) Begin(p *VulnerablePipe) (*VulnerableTransaction, error) {
	req, _, err := p.ReadRequest()
	if err != nil {
		return nil, Opaque(err)
	}
	return &VulnerableTransaction{svc: s, pipe: p, req: req}, nil
}

func (t *VulnerableTransaction) Impersonate() error {
	if err := t.pipe.Impersonate(t.svc.host.Identity); err != nil {
		return Opaque(err)
	}
	t.impersonated = true
	return nil
}

func (t *VulnerableTransaction) Call() (CallOutcome, error) {
	if !t.impersonated {
		return CallOutcome{}, Opaque(errProtocolState)
	}
	return CallOutcome{
		InstanceID: t.pipe.instanceID,
		RequestID:  t.req.ID(),
		Identity:   t.pipe.Call(),
		Payload:    t.req.Bytes(),
	}, nil
}

func (t *VulnerableTransaction) Revert() error {
	t.pipe.Revert()
	return nil
}
