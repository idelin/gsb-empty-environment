package netsec

import (
	"crypto/subtle"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBootstrapTimeout bounds an accepted bootstrap session. Every
// mutation and the Bootstrap-Finish call are rejected once it elapses.
const DefaultBootstrapTimeout = 10 * time.Minute

var (
	// ErrUntrustedServer means the session peer is not an already-trusted
	// bootstrap server, so every write/delete/finish is refused.
	ErrUntrustedServer = errors.New("netsec: bootstrap server is not trusted")
	// ErrSessionClosed means the session was already finished or discarded.
	ErrSessionClosed = errors.New("netsec: bootstrap session is closed")
	// ErrSessionTimeout means the session lifetime elapsed before the call.
	ErrSessionTimeout = errors.New("netsec: bootstrap session timed out")
	// ErrInvalidPath means the LwM2M path is malformed or out of range.
	ErrInvalidPath = errors.New("netsec: invalid LwM2M path")
	// ErrInvalidPayload means a write payload does not match its path level.
	ErrInvalidPayload = errors.New("netsec: invalid write payload")
	// ErrDeleteBootstrapAccount means a delete would remove a bootstrap
	// account, which must survive even a root-level delete.
	ErrDeleteBootstrapAccount = errors.New("netsec: bootstrap accounts cannot be deleted")
	// ErrNoSecureManagementServer means Bootstrap-Finish found no management
	// server account with a security mode other than NoSec.
	ErrNoSecureManagementServer = errors.New("netsec: no management server with a non-NoSec security mode")
)

// Clock abstracts time so timeout behaviour is fully testable.
type Clock func() time.Time

// TrustAnchor describes a bootstrap server the client already trusted before
// the session began. At least one authenticated key is mandatory.
type TrustAnchor struct {
	// ServerURI must equal the URI the client used to reach the server.
	ServerURI string
	// Mode must be a real (non-NoSec) security mode.
	Mode SecurityMode
	// Identity is the PSK identity (/0/x/3) used for PSK mode.
	Identity []byte
	// ServerKey is the authenticated server key (raw public key, certificate
	// fingerprint, or PSK secret for PSK mode).
	ServerKey []byte
}

// SessionInfo describes the peer of an incoming bootstrap session. It is
// filled from the transport handshake (DTLS connection, negotiated cipher
// suite and identity), never from server-supplied object payloads.
type SessionInfo struct {
	// ServerURI is the address the client actually connected to.
	ServerURI string
	// Mode is the negotiated transport security mode. NoSec is never trusted.
	Mode SecurityMode
	// Identity is the authenticated PSK identity when Mode == SecModePSK.
	Identity []byte
	// ServerKey is the key material authenticated during the handshake.
	ServerKey []byte
}

// Guard owns the LwM2M store and mediates every bootstrap-session mutation.
type Guard struct {
	store   *Store
	anchors []TrustAnchor
	timeout time.Duration
	now     Clock

	mu      sync.Mutex
	session *Session
}

// Option customizes a Guard.
type Option func(*Guard)

// WithTimeout overrides the default bootstrap session lifetime.
func WithTimeout(d time.Duration) Option {
	return func(g *Guard) { g.timeout = d }
}

// WithClock injects a clock, mainly for deterministic timeout tests.
func WithClock(clock Clock) Option {
	return func(g *Guard) { g.now = clock }
}

// NewGuard builds an admission guard over store with the set of bootstrap
// servers the client already trusts. Only one bootstrap session is admitted
// at a time.
func NewGuard(store *Store, anchors []TrustAnchor, opts ...Option) *Guard {
	g := &Guard{
		store:   store,
		anchors: cloneAnchors(anchors),
		timeout: DefaultBootstrapTimeout,
		now:     time.Now,
	}
	for _, opt := range opts {
		opt(g)
	}
	return g
}

// Session is one admitted bootstrap session. All changes stay inside the
// session snapshot until Finish validates and commits them.
type Session struct {
	id       uint64
	trusted  bool
	deadline time.Time
	snapshot storeData

	guard *Guard
	open  bool
	mu    sync.Mutex
}

// BeginSession starts a bootstrap session for the given peer. Sessions from
// untrusted peers are created and tracked, but every mutation is rejected so
// a fake server cannot alter the store.
func (g *Guard) BeginSession(info SessionInfo) *Session {
	g.mu.Lock()
	defer g.mu.Unlock()

	var id uint64
	if g.session != nil {
		id = g.session.id + 1
	}
	session := &Session{
		id:       id,
		trusted:  g.isTrusted(info),
		deadline: g.now().Add(g.timeout),
		snapshot: g.store.snapshot(),
		guard:    g,
		open:     true,
	}
	g.session = session
	return session
}

// ID returns the per-guard session sequence number.
func (s *Session) ID() uint64 { return s.id }

// Trusted reports whether the session peer matched a preconfigured bootstrap
// trust anchor.
func (s *Session) Trusted() bool { return s.trusted }

// Active reports whether Finish or Discard has not yet been called.
func (s *Session) Active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.open
}

// Write applies a LwM2M write.
//
//   - object path   "/{obj}"        requires ObjectWrite
//   - instance path "/{obj}/{inst}" requires ResourceSet
//   - resource path "/{obj}/{inst}/{res}" accepts any scalar or []byte value
//
// Writes are rejected for untrusted peers, after timeout, after close and
// for root paths.
func (s *Session) Write(path string, payload any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkMutation(); err != nil {
		return err
	}

	level, obj, inst, res, err := parsePath(path)
	if err != nil {
		return err
	}

	switch level {
	case pathResource:
		s.guard.store.SetResource(obj, inst, res, payload)
	case pathInstance:
		resources, ok := payload.(ResourceSet)
		if !ok {
			return ErrInvalidPayload
		}
		s.guard.store.DeleteInstance(obj, inst)
		for resourceID, value := range resources {
			s.guard.store.SetResource(obj, inst, resourceID, value)
		}
	case pathObject:
		instances, ok := payload.(ObjectWrite)
		if !ok {
			return ErrInvalidPayload
		}
		for instanceID, resources := range instances {
			s.guard.store.DeleteInstance(obj, instanceID)
			for resourceID, value := range resources {
				s.guard.store.SetResource(obj, instanceID, resourceID, value)
			}
		}
	default:
		// Root-level bootstrap writes are not part of the LwM2M flow.
		return ErrInvalidPath
	}
	return nil
}

// Delete applies a LwM2M delete. A root delete ("/") wipes the store while
// preserving every bootstrap account; explicit deletion of a bootstrap
// account instance is refused outright.
func (s *Session) Delete(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkMutation(); err != nil {
		return err
	}

	level, obj, inst, _, err := parsePath(path)
	if err != nil {
		return err
	}

	switch level {
	case pathRoot:
		bootstrapAccounts := s.bootstrapAccounts()
		s.guard.store.DeleteObject(ObjectSecurity)
		s.guard.store.DeleteObject(ObjectServer)
		for accountID, resources := range bootstrapAccounts {
			for resourceID, value := range resources {
				s.guard.store.SetResource(ObjectSecurity, accountID, resourceID, value)
			}
		}
	case pathObject:
		if obj == ObjectSecurity {
			bootstrapAccounts := s.bootstrapAccounts()
			s.guard.store.DeleteObject(ObjectSecurity)
			for accountID, resources := range bootstrapAccounts {
				for resourceID, value := range resources {
					s.guard.store.SetResource(ObjectSecurity, accountID, resourceID, value)
				}
			}
			return nil
		}
		s.guard.store.DeleteObject(obj)
	case pathInstance:
		if obj == ObjectSecurity && s.guard.store.IsBootstrapAccount(inst) {
			return ErrDeleteBootstrapAccount
		}
		s.guard.store.DeleteInstance(obj, inst)
	default:
		return ErrInvalidPath
	}
	return nil
}

// Finish commits the bootstrap session. It first enforces the LwM2M
// Bootstrap-Finish rule: at least one management server account (not a
// bootstrap account) must use a security mode other than NoSec. On failure,
// or after timeout/close, every session change is rolled back.
func (s *Session) Finish() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.open {
		return ErrSessionClosed
	}
	// Admission gates run before any state transition: a rejected Finish
	// leaves the session and the store untouched, mirroring Write/Delete.
	if err := s.checkMutation(); err != nil {
		return err
	}
	s.open = false
	s.guard.detach(s)

	if !s.guard.store.HasSecureManagementAccount() {
		s.guard.store.restore(s.snapshot)
		return ErrNoSecureManagementServer
	}
	return nil
}

// Discard abandons the session and restores the pre-session store state.
// It is idempotent.
func (s *Session) Discard() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.open {
		return
	}
	s.open = false
	s.guard.detach(s)
	s.guard.store.restore(s.snapshot)
}

// checkMutation enforces the common admission gates for write/delete.
func (s *Session) checkMutation() error {
	if !s.open {
		return ErrSessionClosed
	}
	// Timeout is checked before trust: a timed-out session accepts nothing.
	if s.guard.now().After(s.deadline) {
		return ErrSessionTimeout
	}
	if !s.trusted {
		return ErrUntrustedServer
	}
	return nil
}

// bootstrapAccounts returns deep-copied resource maps of every Security
// Object instance flagged as a bootstrap account, keyed by instance ID.
func (s *Session) bootstrapAccounts() map[int]ResourceSet {
	out := map[int]ResourceSet{}
	for _, inst := range s.guard.store.Instances(ObjectSecurity) {
		if !s.guard.store.IsBootstrapAccount(inst) {
			continue
		}
		resources := ResourceSet{}
		for _, resourceID := range s.guard.store.resourceIDs(ObjectSecurity, inst) {
			value, ok := s.guard.store.GetResource(ObjectSecurity, inst, resourceID)
			if ok {
				resources[resourceID] = value
			}
		}
		out[inst] = resources
	}
	return out
}

func (g *Guard) detach(target *Session) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.session == target {
		g.session = nil
	}
}

// isTrusted matches an authenticated session against the anchors provisioned
// before the session. A NoSec peer or a peer without authenticated key
// material can never match.
func (g *Guard) isTrusted(info SessionInfo) bool {
	if info.Mode == SecModeNoSec || len(info.ServerKey) == 0 {
		return false
	}
	for _, anchor := range g.anchors {
		if anchor.Mode == SecModeNoSec || len(anchor.ServerKey) == 0 {
			continue
		}
		if anchor.Mode != info.Mode {
			continue
		}
		if anchor.ServerURI != info.ServerURI {
			continue
		}
		if subtle.ConstantTimeCompare(anchor.ServerKey, info.ServerKey) != 1 {
			continue
		}
		if info.Mode == SecModePSK &&
			subtle.ConstantTimeCompare(anchor.Identity, info.Identity) != 1 {
			continue
		}
		return true
	}
	return false
}

func cloneAnchors(in []TrustAnchor) []TrustAnchor {
	out := make([]TrustAnchor, len(in))
	for i, anchor := range in {
		out[i] = anchor
		if anchor.Identity != nil {
			out[i].Identity = append([]byte(nil), anchor.Identity...)
		}
		if anchor.ServerKey != nil {
			out[i].ServerKey = append([]byte(nil), anchor.ServerKey...)
		}
	}
	return out
}

// TrustAnchorsFromStore builds bootstrap trust anchors from the bootstrap
// accounts already provisioned in a Security Object store. For PSK accounts
// the PSK secret (/0/x/5) is used as the authenticated key; the server
// public key (/0/x/4) is used for RawPK/Certificate accounts.
func TrustAnchorsFromStore(store *Store) []TrustAnchor {
	var anchors []TrustAnchor
	for _, inst := range store.Instances(ObjectSecurity) {
		if !store.IsBootstrapAccount(inst) {
			continue
		}
		modeValue, ok := store.GetResource(ObjectSecurity, inst, ResSecurityMode)
		if !ok {
			continue
		}
		modeNum, ok := toInt(modeValue)
		if !ok || SecurityMode(modeNum) == SecModeNoSec {
			continue
		}
		mode := SecurityMode(modeNum)
		uri, _ := store.GetResource(ObjectSecurity, inst, ResServerURI)
		uriString, _ := uri.(string)

		anchor := TrustAnchor{ServerURI: uriString, Mode: mode}
		if identity, ok := store.GetResource(ObjectSecurity, inst, ResClientIdentity); ok {
			if raw, ok := identity.([]byte); ok {
				anchor.Identity = raw
			}
		}
		keyResource := ResServerPublicKey
		if mode == SecModePSK {
			keyResource = ResSecretKey
		}
		if key, ok := store.GetResource(ObjectSecurity, inst, keyResource); ok {
			if raw, ok := key.([]byte); ok {
				anchor.ServerKey = raw
			}
		}
		if len(anchor.ServerKey) > 0 {
			anchors = append(anchors, anchor)
		}
	}
	return anchors
}

type pathLevel int

const (
	pathRoot pathLevel = iota
	pathObject
	pathInstance
	pathResource
)

// parsePath accepts LwM2M paths: "/", "/{obj}", "/{obj}/{inst}",
// "/{obj}/{inst}/{res}".
func parsePath(path string) (level pathLevel, obj, inst, res int, err error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "/" || trimmed == "" {
		return pathRoot, 0, 0, 0, nil
	}
	if !strings.HasPrefix(trimmed, "/") {
		return 0, 0, 0, 0, ErrInvalidPath
	}
	parts := strings.Split(strings.Trim(trimmed, "/"), "/")
	if len(parts) > 3 {
		return 0, 0, 0, 0, ErrInvalidPath
	}
	ids := make([]int, len(parts))
	for i, part := range parts {
		id, convErr := strconv.Atoi(part)
		if convErr != nil || id < 0 {
			return 0, 0, 0, 0, ErrInvalidPath
		}
		ids[i] = id
	}
	switch len(ids) {
	case 1:
		return pathObject, ids[0], 0, 0, nil
	case 2:
		return pathInstance, ids[0], ids[1], 0, nil
	default:
		return pathResource, ids[0], ids[1], ids[2], nil
	}
}
