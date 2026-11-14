package netsec

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"sync"
	"time"
)

// ReclaimPolicy controls when a quarantined receiver index may return to the
// allocator. All configured gates are conjunctive: the caller waits for the
// old-key message bound, the timeout, and all asynchronous crypto work.
type ReclaimPolicy struct {
	// OldKeyMessageLimit is the number of delayed old-key messages that must
	// be accounted after retirement before reuse. Zero disables the count gate.
	OldKeyMessageLimit uint64
	// RejectAfterMessages is the highest data-message counter that can belong
	// to the old keypair. Accounting a counter at or above this value closes
	// the old-key window. Zero disables the counter gate.
	RejectAfterMessages uint64
	// RejectAfterTime is the minimum quarantine duration. Zero disables the
	// time gate.
	RejectAfterTime time.Duration
}

// DefaultReclaimPolicy mirrors WireGuard's long-lived-key safety bounds. A host
// may call SealLegacyMessages once it knows all old-key messages are gone.
func DefaultReclaimPolicy() ReclaimPolicy {
	return ReclaimPolicy{
		RejectAfterMessages: ^uint64(0) - 8191,
		RejectAfterTime:     180 * time.Second,
	}
}

// IndexAllocator selects a numeric receiver index. The used callback reports
// whether an index already has at least one non-reclaimed binding.
type IndexAllocator interface {
	Allocate(used func(Index) bool) (Index, error)
}

// RandomIndexAllocator avoids index zero, which is reserved as an unspecified
// value at the API boundary.
type RandomIndexAllocator struct {
	Rand        io.Reader
	MaxAttempts int
}

// Allocate implements IndexAllocator.
func (a *RandomIndexAllocator) Allocate(used func(Index) bool) (Index, error) {
	r := a.Rand
	if r == nil {
		r = rand.Reader
	}
	attempts := a.MaxAttempts
	if attempts <= 0 {
		attempts = 256
	}
	var buf [4]byte
	for i := 0; i < attempts; i++ {
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return 0, err
		}
		idx := Index(binary.BigEndian.Uint32(buf[:]))
		if idx == 0 {
			continue
		}
		if used != nil && used(idx) {
			continue
		}
		return idx, nil
	}
	return 0, ErrIndexExhausted
}

// Config configures a Manager. DeviceID is the identity of this tunnel device
// instance. If empty, a random per-manager ID is generated, which still
// prevents bindings from crossing manager instances.
type Config struct {
	DeviceID DeviceID
	Now      func() time.Time
	Rand     io.Reader
	// AuthKey is optional. When set, derived response tuples use HMAC-SHA-256.
	// Hosts that perform the real Noise handshake should provide ResponseAuth
	// explicitly or call SetResponseAuth.
	AuthKey        []byte
	Reclaim        *ReclaimPolicy
	CookieNAT      CookieNATPolicy
	IndexAllocator IndexAllocator
}

// AllocationRequest starts one handshake generation and allocates a receiver
// index capability.
type AllocationRequest struct {
	PeerPublicKey      PublicKey
	EphemeralPublicKey PublicKey
	// Generation is normally zero, which allocates a monotonically increasing
	// generation for the peer. An explicit nonzero value must be greater than
	// the last generation used for that peer.
	Generation uint64
	Nonce      Nonce
	// Index is zero for automatic allocation. A nonzero value selects a
	// specific index; ForceIndex permits that index to collide with an existing
	// binding.
	Index Index
	// ForceIndex creates the deliberate index collision used by diagnostic and
	// adversarial tests. It does not merge identities: both bindings remain
	// separately addressable by their capability handles and authenticated
	// message tuples.
	ForceIndex bool
	// ForceIndexCollision is an alias for ForceIndex.
	ForceIndexCollision bool
	// ResponseAuth is the expected authenticated response tuple. Nil derives
	// one from the binding material.
	ResponseAuth *ResponseAuth
	// CookieAuth optionally binds a cookie expectation at allocation time.
	CookieAuth *CookieAuth
	// Reclaim overrides the manager policy for this binding.
	Reclaim *ReclaimPolicy
}

// AllocateRequest is an alias retained for host adapters.
type AllocateRequest = AllocationRequest

// Manager owns one device instance's receiver-index table and lifecycle state.
type Manager struct {
	mu sync.Mutex

	device    DeviceID
	now       func() time.Time
	rand      io.Reader
	authKey   []byte
	allocator IndexAllocator
	reclaim   ReclaimPolicy
	cookieNAT CookieNATPolicy
	closed    bool

	bindings     map[BindingID]*Binding
	byIndex      map[Index][]*Binding
	byCapability map[Capability]*Binding
	peerLastGen  map[PublicKey]uint64
}

// IndexManager is an alias for Manager used by host adapters.
type IndexManager = Manager

// NewManager constructs a manager. An empty DeviceID is replaced by a random
// device-instance identifier so bindings can never be imported into another
// manager by accident.
func NewManager(cfg Config) (*Manager, error) {
	r := cfg.Rand
	if r == nil {
		r = rand.Reader
	}
	device := cfg.DeviceID
	if device == "" {
		raw, err := randomBytes(r, 16)
		if err != nil {
			return nil, err
		}
		device = DeviceID("dev-" + hex.EncodeToString(raw))
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	reclaim := DefaultReclaimPolicy()
	if cfg.Reclaim != nil {
		reclaim = *cfg.Reclaim
	}
	allocator := cfg.IndexAllocator
	if allocator == nil {
		allocator = &RandomIndexAllocator{Rand: r}
	}
	authKey := append([]byte(nil), cfg.AuthKey...)
	return &Manager{
		device:       device,
		now:          now,
		rand:         r,
		authKey:      authKey,
		allocator:    allocator,
		reclaim:      reclaim,
		cookieNAT:    cfg.CookieNAT,
		bindings:     make(map[BindingID]*Binding),
		byIndex:      make(map[Index][]*Binding),
		byCapability: make(map[Capability]*Binding),
		peerLastGen:  make(map[PublicKey]uint64),
	}, nil
}

// DeviceID returns the device-instance identity captured by the manager.
func (m *Manager) DeviceID() DeviceID {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.device
}

// Allocate creates a binding in InitiationSent state.
func (m *Manager) Allocate(req AllocationRequest) (*Binding, error) {
	if req.PeerPublicKey.IsZero() {
		return nil, ErrPeerPublicKeyRequired
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrManagerClosed
	}

	now := m.now()
	lastGen := m.peerLastGen[req.PeerPublicKey]
	var generation uint64
	if req.Generation == 0 {
		generation = lastGen + 1
		if generation == 0 {
			return nil, ErrGeneration
		}
	} else {
		if req.Generation <= lastGen {
			return nil, ErrGeneration
		}
		generation = req.Generation
	}

	index := req.Index
	if index == 0 {
		allocated, err := m.allocator.Allocate(func(candidate Index) bool {
			return len(m.byIndex[candidate]) > 0
		})
		if err != nil {
			return nil, err
		}
		if allocated == 0 {
			return nil, ErrIndexExhausted
		}
		index = allocated
	} else {
		inUse := len(m.byIndex[index]) > 0
		force := req.ForceIndex || req.ForceIndexCollision
		if inUse && !force {
			return nil, ErrIndexInUse
		}
	}

	nonce := req.Nonce
	if nonce.IsZero() {
		generated, err := randomNonce(m.rand)
		if err != nil {
			return nil, err
		}
		nonce = generated
	}
	ephemeral := req.EphemeralPublicKey
	if ephemeral.IsZero() {
		raw, err := randomBytes(m.rand, len(ephemeral))
		if err != nil {
			return nil, err
		}
		copy(ephemeral[:], raw)
	}
	id, err := randomBindingID(m.rand)
	if err != nil {
		return nil, err
	}

	policy := m.reclaim
	if req.Reclaim != nil {
		policy = *req.Reclaim
	}
	response := deriveResponseAuth(m.authKey, m.device, req.PeerPublicKey, generation, ephemeral, nonce, id, now)
	if req.ResponseAuth != nil {
		response = *req.ResponseAuth
	}
	if req.CookieAuth != nil {
		if req.CookieAuth.Generation != 0 && req.CookieAuth.Generation != generation {
			return nil, ErrGeneration
		}
		if req.CookieAuth.HandshakeHash != (HandshakeHash{}) &&
			req.CookieAuth.HandshakeHash != response.HandshakeHash {
			return nil, ErrHandshakeHashMismatch
		}
	}

	b := &Binding{
		mgr:                 m,
		id:                  id,
		device:              m.device,
		index:               index,
		peer:                req.PeerPublicKey,
		generation:          generation,
		ephemeral:           ephemeral,
		nonce:               nonce,
		createdAt:           now,
		response:            response,
		forceIndex:          (req.ForceIndex || req.ForceIndexCollision) && len(m.byIndex[index]) > 0,
		state:               StateInitiationSent,
		stateChangedAt:      now,
		legacyMessageLimit:  policy.OldKeyMessageLimit,
		rejectAfterMessages: policy.RejectAfterMessages,
		reclaimAfterTime:    policy.RejectAfterTime,
	}
	if req.CookieAuth != nil {
		if !req.CookieAuth.Source.IsValid() {
			return nil, ErrCookieSourceAddressMismatch
		}
		b.cookie = NewCookieAuth(req.CookieAuth.MAC, req.CookieAuth.MessageDigest, req.CookieAuth.Source)
		if req.CookieAuth.Generation == 0 {
			b.cookie.Generation = b.generation
		} else {
			b.cookie.Generation = req.CookieAuth.Generation
		}
		if req.CookieAuth.HandshakeHash == (HandshakeHash{}) {
			b.cookie.HandshakeHash = b.response.HandshakeHash
		} else {
			b.cookie.HandshakeHash = req.CookieAuth.HandshakeHash
		}
		b.hasCookie = true
	}
	b.capability = computeCapability(b.device, b.peer, b.generation, b.ephemeral, b.nonce, b.id)

	m.bindings[b.id] = b
	m.byIndex[b.index] = append(m.byIndex[b.index], b)
	m.byCapability[b.capability] = b
	m.peerLastGen[req.PeerPublicKey] = generation
	return b, nil
}

// BeginHandshake is a convenience wrapper around Allocate for hosts that do
// not need explicit index or generation control.
func (m *Manager) BeginHandshake(peer, ephemeral PublicKey) (*Binding, error) {
	return m.Allocate(AllocationRequest{
		PeerPublicKey:      peer,
		EphemeralPublicKey: ephemeral,
	})
}

// BindingByIndex returns the newest binding carrying the index. The index is a
// routing hint only; packet handlers must call HandleResponse or HandleCookie
// so authentication selects the correct capability.
func (m *Manager) BindingByIndex(index Index) (*Binding, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.byIndex[index]
	if len(list) == 0 {
		return nil, false
	}
	return list[len(list)-1], true
}

// BindingsByIndex returns a snapshot oldest to newest. It is intended for
// diagnostics and tests of forced collisions.
func (m *Manager) BindingsByIndex(index Index) []*Binding {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.byIndex[index]
	out := make([]*Binding, len(list))
	copy(out, list)
	return out
}

// BindingByID resolves an opaque capability handle.
func (m *Manager) BindingByID(id BindingID) (*Binding, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.bindings[id]
	return b, ok
}

// BindingByCapability resolves an immutable capability digest.
func (m *Manager) BindingByCapability(capability Capability) (*Binding, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.byCapability[capability]
	return b, ok
}

// IndexInUse reports whether at least one non-reclaimed binding carries index.
func (m *Manager) IndexInUse(index Index) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.byIndex[index]) > 0
}

// PeerGeneration returns the last generation allocated for peer.
func (m *Manager) PeerGeneration(peer PublicKey) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.peerLastGen[peer]
}

// Activate moves a response-accepted binding to KeypairActive. Activating an
// already active binding is idempotent.
func (m *Manager) Activate(b *Binding) error {
	if b == nil || b.mgr != m {
		return ErrBindingNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.bindings[b.id]; !ok {
		return ErrBindingReclaimed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case StateKeypairActive:
		return nil
	case StateResponseAccepted:
		return b.transitionLocked(StateKeypairActive, m.now(), "keypair active")
	default:
		return ErrInvalidState
	}
}

// Retire moves a binding to Retired without returning its index.
func (m *Manager) Retire(b *Binding, reason string) error {
	if b == nil || b.mgr != m {
		return ErrBindingNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.bindings[b.id]; !ok {
		return ErrBindingReclaimed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case StateRetired, StateQuarantine:
		return nil
	}
	return b.transitionLocked(StateRetired, m.now(), reason)
}

// Quarantine starts the reclaim wait for a retired binding.
func (m *Manager) Quarantine(b *Binding, reason string) error {
	if b == nil || b.mgr != m {
		return ErrBindingNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.bindings[b.id]; !ok {
		return ErrBindingReclaimed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == StateQuarantine {
		return nil
	}
	if b.state != StateRetired {
		return ErrInvalidState
	}
	return b.transitionLocked(StateQuarantine, m.now(), reason)
}

// RetireAndQuarantine is the normal peer-deletion/replacement path.
func (m *Manager) RetireAndQuarantine(b *Binding, reason string) error {
	if b == nil || b.mgr != m {
		return ErrBindingNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.bindings[b.id]; !ok {
		return ErrBindingReclaimed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return m.retireAndQuarantineLocked(b, m.now(), reason)
}

func (m *Manager) retireAndQuarantineLocked(b *Binding, now time.Time, reason string) error {
	switch b.state {
	case StateQuarantine:
		return nil
	case StateRetired:
	default:
		if err := b.transitionLocked(StateRetired, now, reason); err != nil {
			return err
		}
	}
	return b.transitionLocked(StateQuarantine, now, reason)
}

// DeletePeer retires and quarantines every binding for the peer. The peer's
// generation counter is retained, so a recreated peer receives a new
// generation even when the old numeric index is deliberately reused.
func (m *Manager) DeletePeer(peer PublicKey) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	count := 0
	for _, b := range m.bindings {
		if b.peer != peer {
			continue
		}
		b.mu.Lock()
		if !b.reclaimed && b.state != StateQuarantine {
			if err := m.retireAndQuarantineLocked(b, now, "peer deleted"); err == nil {
				count++
			}
		}
		b.mu.Unlock()
	}
	return count
}

// RetireOtherGenerations retires and quarantines all bindings for peer except
// keep. It is useful after a legitimate rekey becomes active.
func (m *Manager) RetireOtherGenerations(peer PublicKey, keep *Binding, reason string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	count := 0
	for _, b := range m.bindings {
		if b.peer != peer || b == keep {
			continue
		}
		b.mu.Lock()
		if !b.reclaimed && b.state != StateQuarantine {
			if err := m.retireAndQuarantineLocked(b, now, reason); err == nil {
				count++
			}
		}
		b.mu.Unlock()
	}
	return count
}

// NoteLegacyMessage accounts for one message belonging to a retired keypair.
// The data-message counter may be zero when the host only tracks a count.
func (m *Manager) NoteLegacyMessage(b *Binding, counter uint64) error {
	if b == nil || b.mgr != m {
		return ErrBindingNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.bindings[b.id]; !ok {
		return ErrBindingReclaimed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state != StateRetired && b.state != StateQuarantine {
		return ErrInvalidState
	}
	b.noteLegacyMessageLocked(counter)
	return nil
}

// SealLegacyMessages declares the old-key message window closed.
func (m *Manager) SealLegacyMessages(b *Binding) error {
	if b == nil || b.mgr != m {
		return ErrBindingNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.bindings[b.id]; !ok {
		return ErrBindingReclaimed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state != StateRetired && b.state != StateQuarantine {
		return ErrInvalidState
	}
	b.legacySealed = true
	return nil
}

// ReclaimStatus explains every gate currently blocking index reuse.
func (m *Manager) ReclaimStatus(b *Binding) ReclaimStatus {
	var status ReclaimStatus
	if b == nil || b.mgr != m {
		return status
	}
	b.mu.RLock()
	if b.reclaimed {
		status.Ready = true
		status.Reclaimed = true
		status.State = b.state
		b.mu.RUnlock()
		return status
	}
	b.mu.RUnlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.bindings[b.id]; !ok {
		return status
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return m.reclaimStatusLocked(b, m.now())
}

func (m *Manager) reclaimStatusLocked(b *Binding, now time.Time) ReclaimStatus {
	status := ReclaimStatus{
		State:                 b.state,
		QuarantinedAt:         b.quarantinedAt,
		EarliestReclaimAt:     b.quarantinedAt.Add(b.reclaimAfterTime),
		LegacyMessages:        b.legacyMessages,
		LegacyMessageLimit:    b.legacyMessageLimit,
		LegacyMaxCounter:      b.legacyMaxCounter,
		OldKeyMessageLimit:    b.legacyMessageLimit,
		OutstandingCryptoWork: b.asyncTasks,
	}
	if b.reclaimed || b.state != StateQuarantine {
		return status
	}
	status.AwaitingTimeout = now.Before(status.EarliestReclaimAt)
	status.AwaitingMessageBound = !b.messageBoundReachedLocked()
	status.AwaitingCrypto = b.asyncTasks != 0
	status.Ready = !status.AwaitingTimeout && !status.AwaitingMessageBound && !status.AwaitingCrypto
	return status
}

// CanReclaim reports whether the binding may leave the index table now.
func (m *Manager) CanReclaim(b *Binding) bool {
	return m.ReclaimStatus(b).Ready
}

// Sweep reclaims all eligible bindings using the manager clock.
func (m *Manager) Sweep() int {
	return m.SweepAt(m.now())
}

// SweepAt reclaims all bindings whose gates are closed at now.
func (m *Manager) SweepAt(now time.Time) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	var ready []*Binding
	for _, b := range m.bindings {
		b.mu.Lock()
		status := m.reclaimStatusLocked(b, now)
		if status.Ready {
			ready = append(ready, b)
		}
		b.mu.Unlock()
	}
	for _, b := range ready {
		b.mu.Lock()
		m.reclaimLocked(b, now)
		b.mu.Unlock()
	}
	return len(ready)
}

// Reclaim removes one binding if all gates are closed, returning the specific
// blocking condition otherwise.
func (m *Manager) Reclaim(b *Binding) error {
	if b == nil || b.mgr != m {
		return ErrBindingNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.bindings[b.id]; !ok {
		return ErrBindingReclaimed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	status := m.reclaimStatusLocked(b, m.now())
	switch {
	case status.Ready:
		m.reclaimLocked(b, m.now())
		return nil
	case b.state != StateQuarantine:
		return fmt.Errorf("%w: state is %s", ErrInvalidState, b.state)
	case status.AwaitingCrypto:
		return ErrAsyncTaskActive
	case status.AwaitingMessageBound:
		return ErrMessageBound
	case status.AwaitingTimeout:
		return ErrReclaimTimeout
	default:
		return ErrInvalidState
	}
}

func (m *Manager) reclaimLocked(b *Binding, now time.Time) {
	if b.reclaimed {
		return
	}
	list := m.byIndex[b.index]
	filtered := list[:0]
	for _, candidate := range list {
		if candidate != b {
			filtered = append(filtered, candidate)
		}
	}
	if len(filtered) == 0 {
		delete(m.byIndex, b.index)
	} else {
		m.byIndex[b.index] = filtered
	}
	delete(m.bindings, b.id)
	delete(m.byCapability, b.capability)
	b.reclaimed = true
	b.reclaimedAt = now
	b.destroyMaterialsLocked()
}

// Close prevents new allocations and message processing. Existing bindings
// remain inspectable; hosts should retire or delete peers first.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
}

// Closed reports whether the manager has been closed.
func (m *Manager) Closed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}
