package cid

import (
	"bytes"
	"fmt"
	"math"
	"net"
	"sync"
)

// LookupFunc finds a session by connection ID.
type LookupFunc func(connectionID []byte) (Session, bool)

// VerifyFunc authenticates and AEAD-opens a record for a session. It must not
// modify the session's peer address. Returning nil certifies that the record is
// authentic and that its sequence number is safe to process.
type VerifyFunc func(session Session, record Record) error

// SendFunc writes payload to addr for session. It returns the number of bytes
// actually written.
type SendFunc func(session Session, addr net.Addr, payload []byte) (int, error)

// ProbeFunc builds the path-validation probe sent after a session address
// change. The returned bytes are sent through SendFunc and count against the
// anti-amplification budget.
type ProbeFunc func(session Session, addr net.Addr) ([]byte, error)

// Config configures a Router.
type Config struct {
	Lookup LookupFunc
	Verify VerifyFunc
	Send   SendFunc
	Probe  ProbeFunc
}

// Router routes received tls12_cid records and enforces migration security.
type Router struct {
	lookup LookupFunc
	verify VerifyFunc
	send   SendFunc
	probe  ProbeFunc

	mu         sync.Mutex
	replay     map[replayKey]*ReplayWindow
	paths      map[string]*pathState
	generation uint64
}

type replayKey struct {
	cid   string
	epoch uint16
}

type pathState struct {
	addr           net.Addr
	addrKey        string
	generation     uint64
	received       uint64
	sent           uint64
	reserved       uint64
	probeScheduled bool
	probeSent      bool
	validated      bool
}

// Result describes a routed record.
type Result struct {
	Record     Record
	Session    Session
	Migrated   bool
	ProbeBytes int
}

// PathStats reports the anti-amplification state for a migrated session.
type PathStats struct {
	Address   net.Addr
	Received  uint64
	Sent      uint64
	Reserved  uint64
	ProbeSent bool
	Validated bool
}

// NewRouter constructs a router. Lookup and Verify are required for records to
// be processed; Send and Probe are required once a session changes address.
func NewRouter(config Config) *Router {
	return &Router{
		lookup: config.Lookup,
		verify: config.Verify,
		send:   config.Send,
		probe:  config.Probe,
		replay: make(map[replayKey]*ReplayWindow),
		paths:  make(map[string]*pathState),
	}
}

// Handle is an alias for Receive.
func (r *Router) Handle(packet []byte, from net.Addr) (Result, error) {
	return r.Receive(packet, from)
}

// Receive routes one complete DTLS record from from. The session peer address
// and path state are not changed until Verify succeeds and the sequence number
// passes the replay window.
func (r *Router) Receive(packet []byte, from net.Addr) (Result, error) {
	record, err := ParseRecord(packet)
	result := Result{Record: record}
	if err != nil {
		return result, err
	}
	if record.ContentType != ContentTypeTLS12CID {
		return result, ErrNotCID
	}
	if from == nil {
		return result, ErrNoSourceAddress
	}
	if r.lookup == nil {
		return result, ErrNoLookup
	}

	session, ok := r.lookup(record.ConnectionID)
	if !ok || session == nil {
		return result, ErrUnknownCID
	}
	result.Session = session

	negotiatedCID := session.ConnectionID()
	if len(negotiatedCID) != len(record.ConnectionID) {
		return result, ErrCIDLengthMismatch
	}
	if !bytes.Equal(negotiatedCID, record.ConnectionID) {
		return result, ErrUnknownCID
	}
	if r.verify == nil {
		return result, ErrNoVerifier
	}
	if err := r.verify(session, record); err != nil {
		return result, fmt.Errorf("%w: %w", ErrAuthentication, err)
	}

	cidKey := string(record.ConnectionID)
	fromKey := addressKey(from)
	var shouldProbe bool
	var probeGeneration uint64

	r.mu.Lock()
	replayWindow := r.replay[replayKey{cid: cidKey, epoch: record.Epoch}]
	if replayWindow == nil {
		replayWindow = new(ReplayWindow)
		r.replay[replayKey{cid: cidKey, epoch: record.Epoch}] = replayWindow
	}
	if !replayWindow.Accept(record.SequenceNumber) {
		r.mu.Unlock()
		return result, ErrReplay
	}

	path := r.paths[cidKey]
	if !sameAddress(session.PeerAddress(), from) {
		r.generation++
		probeGeneration = r.generation
		path = &pathState{
			addr:       from,
			addrKey:    fromKey,
			generation: probeGeneration,
			received:   uint64(len(packet)),
		}
		r.paths[cidKey] = path
		session.SetPeerAddress(from)
		result.Migrated = true
		shouldProbe = true
		path.probeScheduled = true
	} else if path != nil && path.addrKey == fromKey && !path.validated {
		path.received = addSaturating(path.received, uint64(len(packet)))
		if !path.probeSent && !path.probeScheduled {
			shouldProbe = true
			probeGeneration = path.generation
			path.probeScheduled = true
		}
	}
	r.mu.Unlock()

	if shouldProbe {
		sent, probeErr := r.sendPathProbe(session, from, cidKey, probeGeneration)
		result.ProbeBytes = sent
		if probeErr != nil {
			return result, probeErr
		}
	}
	return result, nil
}

func (r *Router) sendPathProbe(session Session, addr net.Addr, cidKey string, generation uint64) (int, error) {
	if r.probe == nil {
		r.cancelScheduledProbe(cidKey, generation)
		return 0, ErrNoProbe
	}
	if r.send == nil {
		r.cancelScheduledProbe(cidKey, generation)
		return 0, ErrNoSend
	}

	probe, err := r.probe(session, addr)
	if err != nil {
		r.cancelScheduledProbe(cidKey, generation)
		return 0, fmt.Errorf("%w: %w", ErrPathProbe, err)
	}
	if len(probe) == 0 {
		r.cancelScheduledProbe(cidKey, generation)
		return 0, fmt.Errorf("%w: empty probe", ErrPathProbe)
	}

	r.mu.Lock()
	path := r.paths[cidKey]
	if path == nil || path.generation != generation || path.addrKey != addressKey(addr) || path.validated {
		if path != nil && path.generation == generation {
			path.probeScheduled = false
		}
		r.mu.Unlock()
		return 0, nil
	}
	if !withinAmplificationLimit(path.received, path.sent, path.reserved, uint64(len(probe))) {
		path.probeScheduled = false
		r.mu.Unlock()
		return 0, ErrAmplificationLimit
	}
	path.probeScheduled = false
	path.reserved += uint64(len(probe))
	r.mu.Unlock()

	written, sendErr := r.send(session, addr, probe)
	written = clampWritten(written, len(probe))

	r.mu.Lock()
	path = r.paths[cidKey]
	if path != nil && path.generation == generation {
		path.reserved -= uint64(len(probe))
		path.sent = addSaturating(path.sent, uint64(written))
		if written > 0 {
			path.probeSent = true
		}
	}
	r.mu.Unlock()

	if sendErr != nil {
		return written, fmt.Errorf("%w: %w", ErrPathProbe, sendErr)
	}
	if written == 0 {
		return 0, fmt.Errorf("%w: probe sent zero bytes", ErrPathProbe)
	}
	return written, nil
}

func (r *Router) cancelScheduledProbe(cidKey string, generation uint64) {
	r.mu.Lock()
	if path := r.paths[cidKey]; path != nil && path.generation == generation {
		path.probeScheduled = false
	}
	r.mu.Unlock()
}

// SendTo sends application bytes to the session's current peer while enforcing
// the 3x anti-amplification limit for an unvalidated migrated path.
func (r *Router) SendTo(session Session, addr net.Addr, payload []byte) (int, error) {
	if session == nil {
		return 0, ErrNoSend
	}
	if r.send == nil {
		return 0, ErrNoSend
	}

	cidKey := string(session.ConnectionID())
	addrKey := addressKey(addr)

	r.mu.Lock()
	if !sameAddress(session.PeerAddress(), addr) {
		r.mu.Unlock()
		return 0, ErrWrongPeerAddress
	}
	path := r.paths[cidKey]
	if path == nil || path.addrKey != addrKey || path.validated {
		r.mu.Unlock()
		return r.send(session, addr, payload)
	}
	if !path.probeSent {
		r.mu.Unlock()
		return 0, ErrPathProbePending
	}
	if !withinAmplificationLimit(path.received, path.sent, path.reserved, uint64(len(payload))) {
		r.mu.Unlock()
		return 0, ErrAmplificationLimit
	}
	generation := path.generation
	path.reserved += uint64(len(payload))
	r.mu.Unlock()

	written, sendErr := r.send(session, addr, payload)
	written = clampWritten(written, len(payload))

	r.mu.Lock()
	path = r.paths[cidKey]
	if path != nil && path.generation == generation && path.addrKey == addrKey {
		path.reserved -= uint64(len(payload))
		path.sent = addSaturating(path.sent, uint64(written))
	}
	r.mu.Unlock()
	return written, sendErr
}

// ConfirmPath marks a path as validated after the caller has authenticated the
// path-probe response. Once confirmed, the anti-amplification limit no longer
// applies to that address.
func (r *Router) ConfirmPath(session Session, addr net.Addr) error {
	if session == nil {
		return ErrNoPendingPath
	}
	cidKey := string(session.ConnectionID())
	addrKey := addressKey(addr)

	r.mu.Lock()
	defer r.mu.Unlock()
	path := r.paths[cidKey]
	if path == nil || path.addrKey != addrKey {
		return ErrNoPendingPath
	}
	path.validated = true
	path.probeScheduled = false
	return nil
}

// ConfirmPathByCID marks a path as validated by connection ID.
func (r *Router) ConfirmPathByCID(connectionID []byte, addr net.Addr) error {
	return r.ConfirmPath(&MemorySession{CID: connectionID}, addr)
}

// PathStats returns the current path state for a session.
func (r *Router) PathStats(session Session) (PathStats, bool) {
	if session == nil {
		return PathStats{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	path := r.paths[string(session.ConnectionID())]
	if path == nil {
		return PathStats{}, false
	}
	return PathStats{
		Address:   path.addr,
		Received:  path.received,
		Sent:      path.sent,
		Reserved:  path.reserved,
		ProbeSent: path.probeSent,
		Validated: path.validated,
	}, true
}

// ForgetSession removes replay and path state for a session.
func (r *Router) ForgetSession(session Session) {
	if session == nil {
		return
	}
	r.ForgetCID(session.ConnectionID())
}

// ForgetCID removes replay and path state for a connection ID.
func (r *Router) ForgetCID(connectionID []byte) {
	cidKey := string(connectionID)
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.paths, cidKey)
	for key := range r.replay {
		if key.cid == cidKey {
			delete(r.replay, key)
		}
	}
}

func sameAddress(a, b net.Addr) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Network() == b.Network() && a.String() == b.String()
}

func addressKey(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	return addr.Network() + "\x00" + addr.String()
}

func withinAmplificationLimit(received, sent, reserved, additional uint64) bool {
	if received > math.MaxUint64/AmplificationFactor {
		return true
	}
	limit := received * AmplificationFactor
	if sent > limit || reserved > limit-sent {
		return false
	}
	return additional <= limit-sent-reserved
}

func addSaturating(a, b uint64) uint64 {
	if a > math.MaxUint64-b {
		return math.MaxUint64
	}
	return a + b
}

func clampWritten(written, length int) int {
	if written < 0 {
		return 0
	}
	if written > length {
		return length
	}
	return written
}
