package netsec

import (
	"encoding/binary"
	"sync"
)

// State is the pipe instance state machine:
//
//	Listening -> Connected -> Reading -> Impersonating -> Connected -> ...
//	any state -> Disconnected -> Listening (instance reuse)
type State int

const (
	StateListening State = iota
	StateConnected
	StateReading
	StateImpersonating
	StateDisconnected
)

func (s State) String() string {
	switch s {
	case StateListening:
		return "Listening"
	case StateConnected:
		return "Connected"
	case StateReading:
		return "Reading"
	case StateImpersonating:
		return "Impersonating"
	case StateDisconnected:
		return "Disconnected"
	}
	return "Unknown"
}

// Identity describes a security principal as bound by the auth layer.
type Identity struct {
	ProcessID uint32
	StartTime int64
	TokenID   uint64
}

// ConnectionSnapshot is the immutable binding produced by Connect. It is
// comparable: two snapshots are equal iff every bound field matches,
// including the instance generation and the per-connection nonce (which
// defeats client PID reuse).
type ConnectionSnapshot struct {
	InstanceID uint64
	Generation uint64
	Identity   Identity
	Nonce      uint64
}

// Request is an immutable view of a decoded request frame.
type Request struct {
	id   uint64
	data []byte
}

func (r *Request) ID() uint64 { return r.id }

// Bytes returns a defensive copy; callers can never mutate the stored
// request bytes.
func (r *Request) Bytes() []byte {
	out := make([]byte, len(r.data))
	copy(out, r.data)
	return out
}

const frameHeaderLen = 12 // 4-byte payload length + 8-byte request id

// EncodeFrame builds a length-prefixed request frame.
func EncodeFrame(requestID uint64, payload []byte) []byte {
	b := make([]byte, frameHeaderLen+len(payload))
	binary.BigEndian.PutUint32(b[0:4], uint32(len(payload)))
	binary.BigEndian.PutUint64(b[4:12], requestID)
	copy(b[frameHeaderLen:], payload)
	return b
}

func decodeFrame(buf []byte) (requestID uint64, payload []byte, ok bool) {
	if len(buf) < frameHeaderLen {
		return 0, nil, false
	}
	n := binary.BigEndian.Uint32(buf[0:4])
	if uint64(len(buf)) < frameHeaderLen+uint64(n) {
		return 0, nil, false
	}
	requestID = binary.BigEndian.Uint64(buf[4:12])
	payload = make([]byte, n)
	copy(payload, buf[frameHeaderLen:frameHeaderLen+n])
	return requestID, payload, true
}

// ImpersonationHandle is a one-shot capability bound to a single connection
// generation of a single pipe instance.
type ImpersonationHandle struct {
	pipe       *PipeInstance
	generation uint64
	seq        uint64
	revoked    bool // guarded by pipe.mu
}

// PipeInstance is one reusable named-pipe instance. Every check that matters
// for security happens under mu, which is the linearization point.
type PipeInstance struct {
	host *Host

	mu         sync.Mutex
	instanceID uint64
	generation uint64
	state      State
	conn       ConnectionSnapshot
	hasConn    bool
	inbound    []byte
	pending    *Request
	consumed   bool
	handle     *ImpersonationHandle
	handleSeq  uint64
}

// State reports the current state (for diagnostics/tests).
func (p *PipeInstance) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// Generation reports the current connection generation.
func (p *PipeInstance) Generation() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.generation
}

// Listen re-arms a disconnected instance. It refuses to listen while any
// stale request or half-consumed frame is still buffered.
func (p *PipeInstance) Listen() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != StateDisconnected {
		return errProtocolState
	}
	if p.pending != nil || len(p.inbound) != 0 {
		return errProtocolState
	}
	p.state = StateListening
	return nil
}

// Connect binds a client identity to this instance. The auth layer supplies
// process id, process start time and token id; the host adds a fresh
// per-connection nonce so a reused PID can never collide with an old
// snapshot.
func (p *PipeInstance) Connect(id Identity) (ConnectionSnapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != StateListening {
		return ConnectionSnapshot{}, errProtocolState
	}
	p.conn = ConnectionSnapshot{
		InstanceID: p.instanceID,
		Generation: p.generation,
		Identity:   id,
		Nonce:      p.host.issueNonce(),
	}
	p.hasConn = true
	p.state = StateConnected
	return p.conn, nil
}

// WriteFromClient appends raw bytes written by the connected client. Bytes
// may arrive in arbitrarily small fragments (half packets).
func (p *PipeInstance) WriteFromClient(chunk []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != StateConnected && p.state != StateReading {
		return errProtocolState
	}
	p.inbound = append(p.inbound, chunk...)
	return nil
}

// ReadRequest decodes one complete frame and returns the immutable request
// together with the single connection snapshot issued for it. Only one
// snapshot is ever produced per request: a second ReadRequest is refused
// until the current request has been fully processed and reverted.
func (p *PipeInstance) ReadRequest() (*Request, ConnectionSnapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != StateConnected {
		return nil, ConnectionSnapshot{}, errProtocolState
	}
	if p.pending != nil {
		return nil, ConnectionSnapshot{}, errProtocolState
	}
	id, payload, ok := decodeFrame(p.inbound)
	if !ok {
		return nil, ConnectionSnapshot{}, errIncompleteFrame
	}
	p.inbound = nil
	p.pending = &Request{id: id, data: payload}
	p.consumed = false
	p.state = StateReading
	return p.pending, p.conn, nil
}

// Impersonate validates, at one linearization point, that the instance is
// still connected, that the presented snapshot matches the live connection
// exactly, and that the pending request has not been consumed. On success it
// returns a one-shot handle bound to the current connection generation.
func (p *PipeInstance) Impersonate(snap ConnectionSnapshot) (*ImpersonationHandle, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != StateConnected && p.state != StateReading {
		return nil, errIdentityMismatch
	}
	if !p.hasConn || p.conn != snap {
		return nil, errIdentityMismatch
	}
	if p.pending == nil {
		return nil, errNoPendingRequest
	}
	if p.consumed {
		return nil, errProtocolState
	}
	p.consumed = true
	p.state = StateImpersonating
	p.handleSeq++
	h := &ImpersonationHandle{pipe: p, generation: p.generation, seq: p.handleSeq}
	p.handle = h
	return h, nil
}

// activeIdentity re-validates the handle immediately before a sensitive
// call: the handle must be the live one, unrevoked, and bound to the
// current generation.
func (p *PipeInstance) activeIdentity(h *ImpersonationHandle) (Identity, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != StateImpersonating || p.handle != h || h == nil || h.revoked || h.generation != p.generation {
		return Identity{}, errHandleRevoked
	}
	return p.conn.Identity, nil
}

// Revert ends impersonation and consumes the handle. The service must not
// process the next request until Revert has succeeded.
func (p *PipeInstance) Revert() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != StateImpersonating || p.handle == nil {
		return errProtocolState
	}
	p.handle.revoked = true
	p.handle = nil
	p.pending = nil
	p.consumed = false
	p.state = StateConnected
	return nil
}

// Disconnect immediately bumps the instance generation, revokes any live
// handle and drops any pending request and partial inbound frame, so the
// instance can be reused without stale state. Old snapshots can never match
// again because the generation moved on.
func (p *PipeInstance) Disconnect() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.generation++
	if p.handle != nil {
		p.handle.revoked = true
		p.handle = nil
	}
	p.pending = nil
	p.consumed = false
	p.hasConn = false
	p.conn = ConnectionSnapshot{}
	p.inbound = nil
	p.state = StateDisconnected
}

// Host owns pipe instances and issues per-connection nonces.
type Host struct {
	Identity Identity // the host process's own identity

	mu             sync.Mutex
	nextInstanceID uint64
	nextNonce      uint64
}

func NewHost(identity Identity) *Host {
	return &Host{Identity: identity}
}

func (h *Host) issueNonce() uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.nextNonce++
	return h.nextNonce
}

// NewInstance creates a pipe instance in the Listening state.
func (h *Host) NewInstance() *PipeInstance {
	h.mu.Lock()
	h.nextInstanceID++
	id := h.nextInstanceID
	h.mu.Unlock()
	return &PipeInstance{host: h, instanceID: id, state: StateListening}
}
