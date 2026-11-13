package netsec

import (
	"bytes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"net"
	"sync"
)

const (
	defaultAmplificationFactor uint64 = 3
	defaultProbeSize                  = 32
)

// Config configures a Router. Zero values select the package defaults.
type Config struct {
	ReplayWindowSize    uint64
	AmplificationFactor uint64
	CIDLengths          []int
	DefaultCIDLength    int
	Authenticator       func(*Session, *Record) ([]byte, error)
	ProbePayload        func(*Session) []byte
	Probe               func(*Session, net.Addr, []byte) error
	Write               func(*Session, net.Addr, []byte) error
}

// PathState is the per-session state for an address change that has not yet
// completed path validation.
type PathState struct {
	Address      net.Addr
	Received     uint64
	Sent         uint64
	Awaiting     bool
	Validated    bool
	ProbeSent    bool
	ProbePending bool
	Probe        []byte
}

// Result describes the outcome of routing one record.
type Result struct {
	Session      *Session
	Record       *Record
	Plaintext    []byte
	Accepted     bool
	Replayed     bool
	Migrated     bool
	ProbeSent    bool
	ProbePending bool
	Probe        []byte
}

// Router maps negotiated CIDs to DTLS sessions and handles authenticated
// tls12_cid records arriving from a (possibly new) source address.
type Router struct {
	mu sync.RWMutex

	sessions map[string]*Session

	ReplayWindowSize    uint64
	AmplificationFactor uint64
	CIDLengths          []int
	DefaultCIDLength    int
	Authenticator       func(*Session, *Record) ([]byte, error)
	ProbePayload        func(*Session) []byte
	Probe               func(*Session, net.Addr, []byte) error
	Transport           func(*Session, net.Addr, []byte) error
}

// CIDRouter is an alias for Router.
type CIDRouter = Router

// NewRouter creates a CID router. Optional configuration is merged left to
// right; omitted values keep package defaults.
func NewRouter(config ...Config) *Router {
	r := &Router{
		sessions:            make(map[string]*Session),
		ReplayWindowSize:    DefaultReplayWindowSize,
		AmplificationFactor: defaultAmplificationFactor,
		DefaultCIDLength:    -1,
	}
	for _, cfg := range config {
		if cfg.ReplayWindowSize > 0 {
			r.ReplayWindowSize = cfg.ReplayWindowSize
		}
		if cfg.AmplificationFactor > 0 {
			r.AmplificationFactor = cfg.AmplificationFactor
		}
		if len(cfg.CIDLengths) > 0 {
			r.CIDLengths = append([]int(nil), cfg.CIDLengths...)
		}
		if cfg.DefaultCIDLength > 0 {
			r.DefaultCIDLength = cfg.DefaultCIDLength
		}
		if cfg.Authenticator != nil {
			r.Authenticator = cfg.Authenticator
		}
		if cfg.ProbePayload != nil {
			r.ProbePayload = cfg.ProbePayload
		}
		if cfg.Probe != nil {
			r.Probe = cfg.Probe
		}
		if cfg.Write != nil {
			r.Transport = cfg.Write
		}
	}
	return r
}

// NewCIDRouter is an alias for NewRouter.
func NewCIDRouter(config ...Config) *Router {
	return NewRouter(config...)
}

// NewServer is an alias for NewRouter.
func NewServer(config ...Config) *Router {
	return NewRouter(config...)
}

// Session is a DTLS association reachable by CID.
type Session struct {
	mu sync.Mutex

	CID                 []byte
	ConnectionID        []byte
	CIDLength           int
	NegotiatedCIDLength int
	Peer                net.Addr
	RemoteAddr          net.Addr
	PeerAddr            net.Addr

	ReplayWindow     *ReplayWindow
	ReplayWindowSize uint64

	AEAD           cipher.AEAD
	NoncePrefix    []byte
	NonceFunc      func(*Record) []byte
	AdditionalData func(*Record) []byte

	Verify        func(*Session, *Record) ([]byte, error)
	Authenticator func(*Record) ([]byte, error)
	Decrypt       func(*Record) ([]byte, error)

	Write        func(net.Addr, []byte) error
	Send         func(net.Addr, []byte) error
	SendProbe    func(net.Addr, []byte) error
	ProbePayload func(*Session) []byte

	path *PathState
}

// NewSession creates a session for cid (string or []byte) and peer.
func NewSession(cid any, peer ...net.Addr) *Session {
	var id []byte
	switch v := cid.(type) {
	case string:
		id = []byte(v)
	case []byte:
		id = append([]byte(nil), v...)
	case nil:
	default:
		panic(fmt.Sprintf("netsec: unsupported CID type %T", cid))
	}
	var addr net.Addr
	if len(peer) > 0 {
		addr = peer[0]
	}
	return &Session{
		CID:          id,
		ConnectionID: append([]byte(nil), id...),
		CIDLength:    len(id),
		Peer:         addr,
		RemoteAddr:   addr,
		PeerAddr:     addr,
	}
}

// Register adds a session to the CID index.
func (r *Router) Register(s *Session) error {
	if s == nil {
		return fmt.Errorf("%w: nil session", ErrSessionNotFound)
	}
	s.normalizeIdentity()
	cidLen := s.effectiveCIDLength()
	if cidLen < 0 || cidLen > 255 {
		return fmt.Errorf("%w: invalid negotiated CID length %d", ErrCIDLength, cidLen)
	}
	if cidLen != len(s.CID) {
		return fmt.Errorf("%w: CID length %d does not match CID value length %d",
			ErrCIDLength, cidLen, len(s.CID))
	}
	if s.Peer == nil && s.RemoteAddr != nil {
		s.Peer = s.RemoteAddr
	}
	if s.RemoteAddr == nil && s.Peer != nil {
		s.RemoteAddr = s.Peer
	}
	if s.Peer == nil && s.PeerAddr != nil {
		s.Peer = s.PeerAddr
	}
	if s.PeerAddr == nil {
		s.PeerAddr = s.Peer
	}
	if s.ReplayWindowSize == 0 {
		s.ReplayWindowSize = r.ReplayWindowSize
	}

	key := string(s.cidValue())
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.sessions[key]; ok && existing != s {
		return fmt.Errorf("%w: duplicate CID", ErrSessionNotFound)
	}
	r.sessions[key] = s
	if s.ReplayWindow == nil {
		s.ReplayWindow = NewReplayWindow(s.ReplayWindowSize)
	}
	return nil
}

// AddSession is an alias for Register.
func (r *Router) AddSession(s *Session) error {
	return r.Register(s)
}

// RegisterSession is an alias for Register.
func (r *Router) RegisterSession(s *Session) error {
	return r.Register(s)
}

// Unregister removes a session from the CID index.
func (r *Router) Unregister(s *Session) {
	if s == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	key := string(s.CID)
	if existing, ok := r.sessions[key]; ok && existing == s {
		delete(r.sessions, key)
	}
}

// RemoveSession is an alias for Unregister.
func (r *Router) RemoveSession(s *Session) {
	r.Unregister(s)
}

// Lookup returns the session registered for cid.
func (r *Router) Lookup(cid []byte) (*Session, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[string(cid)]
	return s, ok
}

// SessionCount returns the number of registered sessions.
func (r *Router) SessionCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sessions)
}

// HandlePacket routes one wire-format datagram/RFC 9146 record. Only records
// whose outer type is tls12_cid (25) are considered.
func (r *Router) HandlePacket(raw []byte, from net.Addr) (*Result, error) {
	if len(raw) == 0 || raw[0] != ContentTypeTLS12CID {
		return nil, ErrNotCID
	}

	if r.DefaultCIDLength >= 0 {
		rec, err := ParseCIDRecord(raw, r.DefaultCIDLength)
		if err != nil {
			return nil, err
		}
		rec.Source = from
		return r.HandleRecord(rec, from)
	}

	lengths := r.candidateCIDLengths()
	if len(lengths) == 0 {
		return nil, ErrSessionNotFound
	}

	sawParsable := false
	for _, length := range lengths {
		rec, err := ParseCIDRecord(raw, length)
		if err != nil {
			continue
		}
		sawParsable = true
		rec.Source = from
		s, ok := r.Lookup(rec.CID)
		if !ok {
			continue
		}
		if length != s.effectiveCIDLength() || !bytes.Equal(rec.CID, s.CID) {
			continue
		}
		return r.HandleRecord(rec, from)
	}
	if sawParsable {
		return nil, ErrSessionNotFound
	}
	return nil, ErrMalformedRecord
}

// Handle is an alias for HandlePacket.
func (r *Router) Handle(raw []byte, from net.Addr) (*Result, error) {
	return r.HandlePacket(raw, from)
}

// ProcessPacket routes a wire record and returns the selected session.
func (r *Router) ProcessPacket(raw []byte, from net.Addr) (*Session, error) {
	res, err := r.HandlePacket(raw, from)
	if err != nil {
		return nil, err
	}
	return res.Session, nil
}

// ProcessRecord routes a parsed record and returns the selected session.
func (r *Router) ProcessRecord(rec *Record, from net.Addr) (*Session, error) {
	res, err := r.HandleRecord(rec, from)
	if err != nil {
		return nil, err
	}
	return res.Session, nil
}

func (r *Router) candidateCIDLengths() []int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	lengths := make([]int, 0, len(r.CIDLengths)+len(r.sessions))
	seen := make(map[int]bool)
	for _, length := range r.CIDLengths {
		if length >= 0 && length <= 255 && !seen[length] {
			seen[length] = true
			lengths = append(lengths, length)
		}
	}
	for _, s := range r.sessions {
		length := s.effectiveCIDLength()
		if !seen[length] {
			seen[length] = true
			lengths = append(lengths, length)
		}
	}
	return lengths
}

// HandleRecord routes an already parsed record. It returns ErrNotCID for
// non-tls12_cid records, ErrCIDLength when the record's CID length differs from
// the negotiated length, and ErrSessionNotFound when the CID is unknown.
// Authentication runs before any peer address, replay-window, or path state is
// modified.
func (r *Router) HandleRecord(input any, from net.Addr) (*Result, error) {
	var rec *Record
	switch v := input.(type) {
	case *Record:
		rec = v
	case Record:
		recCopy := v
		rec = &recCopy
	case []byte:
		return r.HandlePacket(v, from)
	default:
		return nil, fmt.Errorf("%w: unsupported record %T", ErrMalformedRecord, input)
	}
	if rec == nil {
		return nil, ErrMalformedRecord
	}
	rec.normalize()
	if rec.Type != ContentTypeTLS12CID {
		return nil, ErrNotCID
	}
	if rec.CIDLength != 0 && int(rec.CIDLength) != len(rec.CID) {
		return nil, ErrCIDLength
	}

	s, ok := r.Lookup(rec.CID)
	if !ok {
		return nil, ErrSessionNotFound
	}
	if rec.declaredCIDLength() != s.effectiveCIDLength() || len(rec.CID) != s.effectiveCIDLength() {
		return nil, ErrCIDLength
	}

	plaintext, err := r.authenticate(s, rec)
	if err != nil {
		return nil, err
	}

	size := uint64(rec.WireSize())
	if from == nil {
		from = rec.Source
	}

	s.mu.Lock()
	if s.ReplayWindow == nil {
		width := s.ReplayWindowSize
		if width == 0 {
			width = r.ReplayWindowSize
		}
		s.ReplayWindow = NewReplayWindow(width)
	}

	number := rec.RecordNumber()
	res := &Result{
		Session:   s,
		Record:    rec,
		Plaintext: plaintext,
		Accepted:  true,
	}
	if s.ReplayWindow.Seen(number) {
		res.Accepted = false
		res.Replayed = true
		s.mu.Unlock()
		return res, nil
	}
	isNewest := s.ReplayWindow.IsNew(number)
	s.ReplayWindow.Mark(number)

	if from == nil {
		from = s.peerAddr()
	}

	oldPeer := s.peerAddr()
	switch {
	case oldPeer == nil:
		s.setPeerLocked(from)
	case isNewest && !sameAddr(oldPeer, from):
		s.setPeerLocked(from)
		s.path = &PathState{
			Address:  from,
			Received: size,
			Awaiting: true,
			Probe:    r.newProbe(s, rec),
		}
		res.Migrated = true
	case s.path != nil && s.path.Awaiting && sameAddr(s.path.Address, from):
		s.path.Received += size
	}

	probeAddr, probePayload, sendProbe := s.claimProbeLocked(r)
	if s.path != nil {
		res.Probe = append([]byte(nil), s.path.Probe...)
		res.ProbePending = s.path.ProbePending
	}
	s.mu.Unlock()

	if sendProbe {
		err := r.deliverProbe(s, probeAddr, probePayload)
		s.mu.Lock()
		if err != nil {
			if s.path != nil && sameAddr(s.path.Address, probeAddr) {
				s.path.Sent -= uint64(len(probePayload))
				s.path.ProbePending = true
			}
			s.mu.Unlock()
			return res, fmt.Errorf("%w: %v", ErrNoTransport, err)
		}
		if s.path != nil && sameAddr(s.path.Address, probeAddr) {
			s.path.ProbeSent = true
			s.path.ProbePending = false
		}
		s.mu.Unlock()
		res.ProbeSent = true
		res.ProbePending = false
	}
	return res, nil
}

// Route returns the session selected by input (a *Record, Record, or RFC 9146
// wire record) and, when provided, the source address.
func (r *Router) Route(input any, from ...net.Addr) (*Session, error) {
	var addr net.Addr
	if len(from) > 0 {
		addr = from[0]
	}

	switch v := input.(type) {
	case []byte:
		res, err := r.HandlePacket(v, addr)
		if err != nil {
			return nil, err
		}
		return res.Session, nil
	case *Record:
		res, err := r.HandleRecord(v, addr)
		if err != nil {
			return nil, err
		}
		return res.Session, nil
	case Record:
		res, err := r.HandleRecord(&v, addr)
		if err != nil {
			return nil, err
		}
		return res.Session, nil
	default:
		return nil, fmt.Errorf("%w: unsupported input %T", ErrMalformedRecord, input)
	}
}

func (r *Router) authenticate(s *Session, rec *Record) ([]byte, error) {
	var (
		plaintext []byte
		err       error
	)
	switch {
	case s.Verify != nil:
		plaintext, err = s.Verify(s, rec)
	case s.Authenticator != nil:
		plaintext, err = s.Authenticator(rec)
	case s.Decrypt != nil:
		plaintext, err = s.Decrypt(rec)
	case s.AEAD != nil:
		plaintext, err = s.openRecord(rec)
	case r.Authenticator != nil:
		plaintext, err = r.Authenticator(s, rec)
	default:
		return nil, ErrNoAuthenticator
	}
	if err != nil {
		if isAuthenticationError(err) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrAuthentication, err)
	}
	return plaintext, nil
}

func isAuthenticationError(err error) bool {
	type unwrapper interface{ Unwrap() error }
	for err != nil {
		if err == ErrAuthentication {
			return true
		}
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func (r *Router) newProbe(s *Session, rec *Record) []byte {
	if s.ProbePayload != nil {
		if p := s.ProbePayload(s); len(p) > 0 {
			return append([]byte(nil), p...)
		}
	}
	if r.ProbePayload != nil {
		if p := r.ProbePayload(s); len(p) > 0 {
			return append([]byte(nil), p...)
		}
	}
	p := make([]byte, defaultProbeSize)
	if _, err := rand.Read(p); err != nil {
		for i := range p {
			p[i] = byte(rec.RecordNumber() >> (uint(i%8) * 8))
		}
	}
	return p
}

func (s *Session) claimProbeLocked(r *Router) (net.Addr, []byte, bool) {
	if s.path == nil || !s.path.Awaiting || s.path.ProbeSent {
		return nil, nil, false
	}
	if len(s.path.Probe) == 0 {
		s.path.ProbePending = true
		return nil, nil, false
	}
	factor := r.AmplificationFactor
	if factor == 0 {
		factor = defaultAmplificationFactor
	}
	probeLen := uint64(len(s.path.Probe))
	if s.path.Sent+probeLen > factor*s.path.Received {
		s.path.ProbePending = true
		return nil, nil, false
	}
	s.path.Sent += probeLen
	return s.path.Address, append([]byte(nil), s.path.Probe...), true
}

func (r *Router) deliverProbe(s *Session, addr net.Addr, payload []byte) error {
	switch {
	case s.SendProbe != nil:
		return s.SendProbe(addr, payload)
	case r.Probe != nil:
		return r.Probe(s, addr, payload)
	case s.Write != nil:
		return s.Write(addr, payload)
	case s.Send != nil:
		return s.Send(addr, payload)
	case r.Transport != nil:
		return r.Transport(s, addr, payload)
	default:
		return nil
	}
}

// Send writes data to the session's current peer. While a changed address
// awaits its path validation response, the cumulative number of bytes sent to
// that address may not exceed AmplificationFactor times the authenticated
// bytes received from it.
func (r *Router) Send(s *Session, data []byte) error {
	if s == nil {
		return ErrSessionNotFound
	}

	s.mu.Lock()
	addr := s.peerAddr()
	var reserved bool
	if s.path != nil && s.path.Awaiting {
		factor := r.AmplificationFactor
		if factor == 0 {
			factor = defaultAmplificationFactor
		}
		n := uint64(len(data))
		if s.path.Sent+n > factor*s.path.Received {
			s.mu.Unlock()
			return ErrAmplification
		}
		s.path.Sent += n
		reserved = true
	}
	write := s.Write
	if write == nil {
		write = s.Send
	}
	if write == nil && r.Transport != nil {
		write = func(a net.Addr, p []byte) error {
			return r.Transport(s, a, p)
		}
	}
	if write == nil {
		write = func(net.Addr, []byte) error { return nil }
	}
	s.mu.Unlock()

	err := write(addr, data)
	if err != nil && reserved {
		s.mu.Lock()
		if s.path != nil {
			s.path.Sent -= uint64(len(data))
		}
		s.mu.Unlock()
	}
	return err
}

// Write is an alias for Send.
func (r *Router) Write(s *Session, data []byte) error {
	return r.Send(s, data)
}

// SendTo writes data only to the session's current peer. It exists so callers
// that already have a destination can use the same budget checks.
func (r *Router) SendTo(s *Session, addr net.Addr, data []byte) error {
	if s == nil {
		return ErrSessionNotFound
	}
	s.mu.Lock()
	current := s.peerAddr()
	s.mu.Unlock()
	if current != nil && !sameAddr(current, addr) {
		return ErrWrongPath
	}
	return r.Send(s, data)
}

// WriteTo is an alias for SendTo.
func (r *Router) WriteTo(s *Session, addr net.Addr, data []byte) error {
	return r.SendTo(s, addr, data)
}

// ConfirmPath marks the pending path as validated. A non-empty response is
// compared against the probe payload. Once confirmed, the amplification limit
// for the new address is lifted.
func (r *Router) ConfirmPath(s *Session, from net.Addr, response []byte) error {
	if s == nil {
		return ErrSessionNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == nil || !s.path.Awaiting {
		return nil
	}
	if !sameAddr(s.path.Address, from) {
		return ErrWrongPath
	}
	if len(response) > 0 && subtle.ConstantTimeCompare(response, s.path.Probe) != 1 {
		return ErrBadPathResponse
	}
	s.path.Awaiting = false
	s.path.Validated = true
	s.path.ProbePending = false
	return nil
}

// HandlePathResponse is an alias for ConfirmPath.
func (r *Router) HandlePathResponse(s *Session, from net.Addr, response []byte) error {
	return r.ConfirmPath(s, from, response)
}

// PathResponse is an alias for ConfirmPath.
func (r *Router) PathResponse(s *Session, from net.Addr, response []byte) error {
	return r.ConfirmPath(s, from, response)
}

// Path returns a snapshot of the session's path-validation state.
func (s *Session) Path() (PathState, bool) {
	if s == nil {
		return PathState{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == nil {
		return PathState{}, false
	}
	out := *s.path
	out.Probe = append([]byte(nil), s.path.Probe...)
	return out, true
}

// PathState returns a snapshot of the session's path-validation state.
func (s *Session) PathState() (PathState, bool) {
	return s.Path()
}

// LastProbe returns the most recent path validation probe payload.
func (s *Session) LastProbe() []byte {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == nil {
		return nil
	}
	return append([]byte(nil), s.path.Probe...)
}

// AwaitingPathValidation reports whether a changed address still needs its
// path validation response.
func (s *Session) AwaitingPathValidation() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.path != nil && s.path.Awaiting
}

// SetPeer updates the current peer address.
func (s *Session) SetPeer(addr net.Addr) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.setPeerLocked(addr)
}

// PeerAddress returns the current peer address.
func (s *Session) PeerAddress() net.Addr {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peerAddr()
}

func (s *Session) effectiveCIDLength() int {
	if s.CIDLength > 0 {
		return s.CIDLength
	}
	if s.NegotiatedCIDLength > 0 {
		return s.NegotiatedCIDLength
	}
	return len(s.cidValue())
}

func (s *Session) peerAddr() net.Addr {
	if s.Peer != nil {
		return s.Peer
	}
	if s.RemoteAddr != nil {
		return s.RemoteAddr
	}
	return s.PeerAddr
}

func (s *Session) setPeerLocked(addr net.Addr) {
	s.Peer = addr
	s.RemoteAddr = addr
	s.PeerAddr = addr
}

func (s *Session) cidValue() []byte {
	if s.CID != nil {
		return s.CID
	}
	return s.ConnectionID
}

func (s *Session) normalizeIdentity() {
	if s.CID == nil && s.ConnectionID != nil {
		s.CID = append([]byte(nil), s.ConnectionID...)
	}
	if s.ConnectionID == nil {
		s.ConnectionID = append([]byte(nil), s.CID...)
	}
}

func sameAddr(a, b net.Addr) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.Network() != b.Network() {
		return false
	}
	return a.String() == b.String()
}
