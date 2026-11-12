package netsec

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"io"
	"sort"
	"sync"
)

// Isolation owns all tenant state for a multiplexed HSM slot. It turns the
// slot-wide object namespace into one private namespace per tenant and
// enforces the visibility rules that the underlying PKCS#11 implementation
// cannot enforce when sessions from several tenants share one application
// login state.
type Isolation struct {
	mu sync.RWMutex

	nextSessionID uint64
	tenants       map[TenantID]*tenantState
}

type tenantState struct {
	id TenantID

	pinHash       [sha256.Size]byte
	pinConfigured bool
	loggedIn      bool

	sessions     map[uint64]*Session
	tokenObjects map[Handle]*storedObject
	usedHandles  map[Handle]struct{}
}

// Session is a tenant-owned handle space. A Session never resolves an object
// through another tenant's namespace, and it resolves session objects only
// through the map of the Session that created them.
type Session struct {
	iso    *Isolation
	tenant TenantID
	id     uint64
	closed bool

	objects map[Handle]*storedObject
}

// NewIsolation creates an empty isolation domain for a shared slot.
func NewIsolation() *Isolation {
	return &Isolation{
		tenants: make(map[TenantID]*tenantState),
	}
}

// NewBroker is an alias for NewIsolation.
func NewBroker() *Isolation { return NewIsolation() }

// NewSessionIsolation is an alias for NewIsolation.
func NewSessionIsolation() *Isolation { return NewIsolation() }

// RegisterTenant provisions a tenant and optionally configures the PIN that
// Login must present. If pin is omitted, authentication is delegated to the
// proxy/HSM layer and Login accepts the supplied value.
func (i *Isolation) RegisterTenant(id TenantID, pin ...string) error {
	if id == "" {
		return ErrInvalidTenant
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	if _, exists := i.tenants[id]; exists {
		return ErrTenantExists
	}
	t := newTenantState(id)
	if len(pin) > 0 {
		t.setPIN(pin[0])
	}
	i.tenants[id] = t
	return nil
}

// SetPIN configures the PIN for a tenant, creating tenant state if necessary.
// It does not change an existing login state.
func (i *Isolation) SetPIN(id TenantID, pin string) error {
	if id == "" {
		return ErrInvalidTenant
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	t := i.tenantLocked(id)
	t.setPIN(pin)
	return nil
}

// OpenSession opens a session for a tenant. Tenant state is created on first
// use; callers that require PIN validation should provision the tenant first.
func (i *Isolation) OpenSession(tenantID TenantID) (*Session, error) {
	if tenantID == "" {
		return nil, ErrInvalidTenant
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	t := i.tenantLocked(tenantID)
	id := i.nextSessionID
	i.nextSessionID++

	s := &Session{
		iso:     i,
		tenant:  tenantID,
		id:      id,
		objects: make(map[Handle]*storedObject),
	}
	t.sessions[id] = s
	return s, nil
}

// NewSession is an alias for OpenSession.
func (i *Isolation) NewSession(tenantID TenantID) (*Session, error) {
	return i.OpenSession(tenantID)
}

// RemoveTenant closes every session and destroys every object belonging to
// the tenant. Token objects are destroyed as well because the tenant itself is
// being removed from the proxy.
func (i *Isolation) RemoveTenant(tenantID TenantID) error {
	if tenantID == "" {
		return ErrInvalidTenant
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	t, ok := i.tenants[tenantID]
	if !ok {
		return ErrInvalidTenant
	}
	for _, session := range t.sessions {
		session.closed = true
		clear(session.objects)
	}
	clear(t.tokenObjects)
	clear(t.usedHandles)
	clear(t.sessions)
	t.loggedIn = false
	delete(i.tenants, tenantID)
	return nil
}

// ID returns the session identifier. It is only meaningful inside the
// isolation domain and is never used as an object handle.
func (s *Session) ID() uint64 { return s.id }

// Tenant returns the authenticated tenant that owns this session.
func (s *Session) Tenant() TenantID { return s.tenant }

// LoggedIn reports whether the owning tenant has an active login. Login state
// is never shared with another tenant.
func (s *Session) LoggedIn() bool {
	s.iso.mu.RLock()
	defer s.iso.mu.RUnlock()

	t, err := s.stateLocked()
	if err != nil {
		return false
	}
	return t.loggedIn
}

// Login marks the tenant as logged in. It affects every session of that
// tenant, but no other tenant's sessions. If a PIN was configured with
// RegisterTenant or SetPIN, pin must match it.
func (s *Session) Login(pin string) error {
	s.iso.mu.Lock()
	defer s.iso.mu.Unlock()

	t, err := s.stateLocked()
	if err != nil {
		return err
	}
	if t.pinConfigured {
		sum := sha256.Sum256([]byte(pin))
		if subtle.ConstantTimeCompare(sum[:], t.pinHash[:]) != 1 {
			return ErrInvalidPIN
		}
	}
	t.loggedIn = true
	return nil
}

// Logout logs the whole tenant out. Every session object created by any
// session of the tenant is destroyed immediately, across all of the tenant's
// still-open sessions. Token objects are retained but become invisible until
// the tenant logs in again. Other tenants are unaffected.
func (s *Session) Logout() error {
	s.iso.mu.Lock()
	defer s.iso.mu.Unlock()

	t, err := s.stateLocked()
	if err != nil {
		return err
	}

	for _, session := range t.sessions {
		for handle := range session.objects {
			delete(t.usedHandles, handle)
		}
		clear(session.objects)
	}
	t.loggedIn = false
	return nil
}

// CreateObject creates an object in the tenant's private handle space. Token
// objects require an active tenant login; session objects do not.
func (s *Session) CreateObject(attrs Attributes) (Handle, error) {
	s.iso.mu.Lock()
	defer s.iso.mu.Unlock()

	t, err := s.stateLocked()
	if err != nil {
		return 0, err
	}
	return s.createObjectLocked(t, attrs)
}

// CreateSessionObject creates a CKA_TOKEN=false object. It is visible only
// through this Session until it is destroyed, the session is closed, or the
// tenant logs out.
func (s *Session) CreateSessionObject(attrs Attributes) (Handle, error) {
	attrs.Token = false
	return s.CreateObject(attrs)
}

// CreateTokenObject creates a CKA_TOKEN=true object. The tenant must be
// logged in. After creation, all sessions of the same tenant can access it
// while the tenant remains logged in.
func (s *Session) CreateTokenObject(attrs Attributes) (Handle, error) {
	attrs.Token = true
	return s.CreateObject(attrs)
}

// GetObject resolves a handle in this tenant and session namespace and returns
// a snapshot of the object. A handle that does not exist and a handle owned by
// another tenant both return the identical ErrObjectNotFound value.
func (s *Session) GetObject(handle Handle) (Object, error) {
	s.iso.mu.RLock()
	defer s.iso.mu.RUnlock()

	t, err := s.stateLocked()
	if err != nil {
		return Object{}, err
	}
	o := s.lookupObjectLocked(t, handle)
	if o == nil {
		return Object{}, ErrObjectNotFound
	}
	return o.snapshot(), nil
}

// LookupObject is an alias for GetObject.
func (s *Session) LookupObject(handle Handle) (Object, error) {
	return s.GetObject(handle)
}

// FindObjects returns authorized objects matching filter. It lists only
// objects owned by this tenant: session objects from this Session and, when
// the tenant is logged in, token objects of this tenant.
func (s *Session) FindObjects(filter Filter) ([]Object, error) {
	s.iso.mu.RLock()
	defer s.iso.mu.RUnlock()

	t, err := s.stateLocked()
	if err != nil {
		return nil, err
	}

	objects := s.visibleObjectsLocked(t, filter)
	out := make([]Object, 0, len(objects))
	for _, o := range objects {
		out = append(out, o.snapshot())
	}
	return out, nil
}

// FindHandles returns the handles of objects matching filter, in ascending
// numeric order. The handles are still private to this tenant and session.
func (s *Session) FindHandles(filter Filter) ([]Handle, error) {
	s.iso.mu.RLock()
	defer s.iso.mu.RUnlock()

	t, err := s.stateLocked()
	if err != nil {
		return nil, err
	}

	objects := s.visibleObjectsLocked(t, filter)
	out := make([]Handle, 0, len(objects))
	for _, o := range objects {
		out = append(out, o.handle)
	}
	return out, nil
}

// DestroyObject destroys a visible object. A session object can only be
// destroyed through the Session that created it. A token object can be
// destroyed through any session of the owning tenant while that tenant is
// logged in.
func (s *Session) DestroyObject(handle Handle) error {
	s.iso.mu.Lock()
	defer s.iso.mu.Unlock()

	t, err := s.stateLocked()
	if err != nil {
		return err
	}

	if o, ok := s.objects[handle]; ok &&
		o.ownerTenant == s.tenant && o.ownerSession == s.id {
		delete(s.objects, handle)
		delete(t.usedHandles, handle)
		return nil
	}
	if t.loggedIn {
		if o, ok := t.tokenObjects[handle]; ok && o.ownerTenant == s.tenant {
			delete(t.tokenObjects, handle)
			delete(t.usedHandles, handle)
			return nil
		}
	}
	return ErrObjectNotFound
}

// Close closes the session and destroys all session objects it created. If
// this was the tenant's last open session, the tenant is logged out; token
// objects remain stored for a future login.
func (s *Session) Close() error {
	s.iso.mu.Lock()
	defer s.iso.mu.Unlock()

	if s.closed {
		return ErrSessionClosed
	}
	t, ok := s.iso.tenants[s.tenant]
	if !ok {
		s.closed = true
		return ErrSessionClosed
	}

	for handle := range s.objects {
		delete(t.usedHandles, handle)
	}
	clear(s.objects)
	delete(t.sessions, s.id)
	s.closed = true

	if len(t.sessions) == 0 {
		t.loggedIn = false
	}
	return nil
}

// ActiveSessions returns the number of open sessions owned by the tenant.
func (s *Session) ActiveSessions() int {
	s.iso.mu.RLock()
	defer s.iso.mu.RUnlock()

	t, err := s.stateLocked()
	if err != nil {
		return 0
	}
	return len(t.sessions)
}

func newTenantState(id TenantID) *tenantState {
	return &tenantState{
		id:           id,
		sessions:     make(map[uint64]*Session),
		tokenObjects: make(map[Handle]*storedObject),
		usedHandles:  make(map[Handle]struct{}),
	}
}

func (t *tenantState) setPIN(pin string) {
	t.pinHash = sha256.Sum256([]byte(pin))
	t.pinConfigured = true
}

// tenantLocked returns the tenant state, creating it on first use. The caller
// must hold i.mu for writing.
func (i *Isolation) tenantLocked(id TenantID) *tenantState {
	if t, ok := i.tenants[id]; ok {
		return t
	}
	t := newTenantState(id)
	i.tenants[id] = t
	return t
}

// stateLocked verifies that the session still belongs to this isolation
// domain. The caller must hold s.iso.mu.
func (s *Session) stateLocked() (*tenantState, error) {
	if s.closed {
		return nil, ErrSessionClosed
	}
	t, ok := s.iso.tenants[s.tenant]
	if !ok {
		return nil, ErrSessionClosed
	}
	return t, nil
}

func (s *Session) createObjectLocked(t *tenantState, attrs Attributes) (Handle, error) {
	if attrs.Token && !t.loggedIn {
		return 0, ErrNotLoggedIn
	}

	handle, err := s.newHandleLocked(t)
	if err != nil {
		return 0, err
	}
	o := newStoredObject(handle, attrs, s.tenant, s.id)
	if attrs.Token {
		t.tokenObjects[handle] = o
	} else {
		s.objects[handle] = o
	}
	t.usedHandles[handle] = struct{}{}
	return handle, nil
}

func (s *Session) newHandleLocked(t *tenantState) (Handle, error) {
	var buf [8]byte
	for attempts := 0; attempts < 64; attempts++ {
		if _, err := io.ReadFull(rand.Reader, buf[:]); err != nil {
			return 0, fmt.Errorf("%w: %v", ErrEntropy, err)
		}
		handle := Handle(binary.BigEndian.Uint64(buf[:]))
		if handle == 0 {
			continue
		}
		if _, used := t.usedHandles[handle]; used {
			continue
		}
		return handle, nil
	}
	return 0, ErrEntropy
}

func (s *Session) lookupObjectLocked(t *tenantState, handle Handle) *storedObject {
	if o, ok := s.objects[handle]; ok &&
		o.ownerTenant == s.tenant && o.ownerSession == s.id {
		return o
	}
	if t.loggedIn {
		if o, ok := t.tokenObjects[handle]; ok && o.ownerTenant == s.tenant {
			return o
		}
	}
	return nil
}

func (s *Session) visibleObjectsLocked(t *tenantState, filter Filter) []*storedObject {
	objects := make([]*storedObject, 0, len(s.objects)+len(t.tokenObjects))
	for _, o := range s.objects {
		if o.ownerTenant == s.tenant && o.ownerSession == s.id && o.matches(filter) {
			objects = append(objects, o)
		}
	}
	if t.loggedIn {
		for _, o := range t.tokenObjects {
			if o.ownerTenant == s.tenant && o.matches(filter) {
				objects = append(objects, o)
			}
		}
	}
	sort.Slice(objects, func(a, b int) bool {
		return objects[a].handle < objects[b].handle
	})
	return objects
}
