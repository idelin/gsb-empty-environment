package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"net/netip"
	"sync"
	"time"
)

// Binding is one immutable receiver-index capability plus its mutable
// lifecycle state. All identifying fields are captured when the binding is
// allocated. A caller must retain the binding (or its opaque BindingID) rather
// than re-resolving work through a numeric receiver index.
type Binding struct {
	mgr *Manager

	id         BindingID
	device     DeviceID
	index      Index
	peer       PublicKey
	generation uint64
	ephemeral  PublicKey
	nonce      Nonce
	createdAt  time.Time
	capability Capability
	response   ResponseAuth
	cookie     CookieAuth
	hasCookie  bool
	forceIndex bool

	mu               sync.RWMutex
	state            LifecycleState
	stateChangedAt   time.Time
	acceptedAt       time.Time
	activatedAt      time.Time
	retiredAt        time.Time
	quarantinedAt    time.Time
	reclaimedAt      time.Time
	retireReason     string
	quarantineReason string
	reclaimed        bool
	materialsWiped   bool

	asyncTasks    int
	asyncSequence uint64

	legacyMessages      uint64
	legacyMaxCounter    uint64
	legacyMessageLimit  uint64
	rejectAfterMessages uint64
	reclaimAfterTime    time.Duration
	legacySealed        bool

	cookiesSeen  uint64
	lastCookieAt time.Time
}

// ID returns the opaque capability handle. It is never the numeric receiver
// index.
func (b *Binding) ID() BindingID { return b.id }

// Index returns the numeric receiver index. The same value can temporarily be
// shared by an old and a new binding during an explicitly forced collision.
func (b *Binding) Index() Index { return b.index }

// DeviceID returns the device instance to which this capability is bound.
func (b *Binding) DeviceID() DeviceID { return b.device }

// Device is an alias for DeviceID.
func (b *Binding) Device() DeviceID { return b.device }

// PeerPublicKey returns the peer static public key captured at allocation.
func (b *Binding) PeerPublicKey() PublicKey { return b.peer }

// HandshakeGeneration returns the generation captured at allocation.
func (b *Binding) HandshakeGeneration() uint64 { return b.generation }

// Generation is an alias for HandshakeGeneration.
func (b *Binding) Generation() uint64 { return b.generation }

// EphemeralPublicKey returns the temporary handshake key. It becomes the zero
// key after the binding is retired and all cryptographic work has drained.
func (b *Binding) EphemeralPublicKey() PublicKey {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.ephemeral
}

// TemporaryKey is an alias for EphemeralPublicKey.
func (b *Binding) TemporaryKey() PublicKey { return b.EphemeralPublicKey() }

// CreationNonce returns the random value created with the binding.
func (b *Binding) CreationNonce() Nonce {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.nonce
}

// Nonce is an alias for CreationNonce.
func (b *Binding) Nonce() Nonce { return b.CreationNonce() }

// Capability returns the immutable digest of the binding identity.
func (b *Binding) Capability() Capability { return b.capability }

// CreatedAt returns the allocation time.
func (b *Binding) CreatedAt() time.Time { return b.createdAt }

// State returns the current lifecycle state.
func (b *Binding) State() LifecycleState {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.state
}

// StateChangedAt returns the time of the last lifecycle transition.
func (b *Binding) StateChangedAt() time.Time {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.stateChangedAt
}

// AcceptedAt returns the time a matching response was accepted.
func (b *Binding) AcceptedAt() time.Time {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.acceptedAt
}

// ActivatedAt returns the time the keypair became active.
func (b *Binding) ActivatedAt() time.Time {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.activatedAt
}

// RetiredAt returns the time the binding was retired.
func (b *Binding) RetiredAt() time.Time {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.retiredAt
}

// QuarantinedAt returns the time the binding entered quarantine.
func (b *Binding) QuarantinedAt() time.Time {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.quarantinedAt
}

// ReclaimedAt returns the time the index was returned to the allocator.
func (b *Binding) ReclaimedAt() time.Time {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.reclaimedAt
}

// Reclaimed reports whether this binding has left the index table.
func (b *Binding) Reclaimed() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.reclaimed
}

// HasMaterials reports whether temporary key material is still present.
func (b *Binding) HasMaterials() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return !b.materialsWiped
}

// AsyncTasks returns the number of unfinished asynchronous crypto tasks.
func (b *Binding) AsyncTasks() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.asyncTasks
}

// LegacyMessages returns the number of old-key messages accounted after
// retirement.
func (b *Binding) LegacyMessages() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.legacyMessages
}

// LegacyMaxCounter returns the highest old-key data-message counter observed.
func (b *Binding) LegacyMaxCounter() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.legacyMaxCounter
}

// LegacyMessageLimit returns the configured count gate. Zero means the count
// gate is disabled.
func (b *Binding) LegacyMessageLimit() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.legacyMessageLimit
}

// OldKeyMessageLimit is an alias for LegacyMessageLimit.
func (b *Binding) OldKeyMessageLimit() uint64 { return b.LegacyMessageLimit() }

// CookiesSeen returns the number of accepted cookie replies for this binding.
func (b *Binding) CookiesSeen() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.cookiesSeen
}

// LastCookieAt returns the time of the last accepted cookie reply.
func (b *Binding) LastCookieAt() time.Time {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.lastCookieAt
}

// ResponseAuth returns a copy of the expected response authentication tuple.
func (b *Binding) ResponseAuth() ResponseAuth {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.response
}

// CookieAuth returns a copy of the expected cookie authentication tuple.
func (b *Binding) CookieAuth() CookieAuth {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.cookie
}

// HasCookieAuth reports whether a cookie expectation is bound.
func (b *Binding) HasCookieAuth() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.hasCookie
}

// MatchesIdentity reports whether all immutable identity fields match. This
// check intentionally does not consult the numeric index table.
func (b *Binding) MatchesIdentity(device DeviceID, peer PublicKey, generation uint64, ephemeral PublicKey, nonce Nonce) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.device == device &&
		b.peer == peer &&
		b.generation == generation &&
		b.ephemeral == ephemeral &&
		b.nonce == nonce
}

// ForcedCollision reports whether this binding was deliberately allocated
// with a numeric index already used by another binding.
func (b *Binding) ForcedCollision() bool { return b.forceIndex }

// RetireReason returns the reason supplied when the binding was retired.
func (b *Binding) RetireReason() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.retireReason
}

// QuarantineReason returns the reason supplied when the binding entered
// quarantine.
func (b *Binding) QuarantineReason() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.quarantineReason
}

// Retire moves the binding to Retired. Old temporary material is retained
// until asynchronous work drains so a late task cannot use freed material.
func (b *Binding) Retire(reason string) error {
	return b.mgr.Retire(b, reason)
}

// Quarantine moves a retired binding into the reclaim wait state.
func (b *Binding) Quarantine(reason string) error {
	return b.mgr.Quarantine(b, reason)
}

// RetireAndQuarantine is the normal deletion path.
func (b *Binding) RetireAndQuarantine(reason string) error {
	return b.mgr.RetireAndQuarantine(b, reason)
}

// Activate moves a response-accepted binding to KeypairActive.
func (b *Binding) Activate() error {
	return b.mgr.Activate(b)
}

// SealLegacyMessages declares that no further old-key messages can arrive.
// This is useful when the host knows the removed key's counter window is
// closed.
func (b *Binding) SealLegacyMessages() error {
	return b.mgr.SealLegacyMessages(b)
}

// NoteLegacyMessage accounts for one delayed old-key message. counter is the
// data-message counter when available.
func (b *Binding) NoteLegacyMessage(counter uint64) error {
	return b.mgr.NoteLegacyMessage(b, counter)
}

// StartCryptoTask pins temporary key material for asynchronous work. The task
// must be finished before the index can be reclaimed.
func (b *Binding) StartCryptoTask() (*CryptoTask, error) {
	return b.mgr.StartCryptoTask(b)
}

// DestroyMaterials zeroes temporary key material owned by a retired binding.
// It never consults the index table. Destroying material on a live binding is
// rejected: asynchronous work and packet processing must not observe a
// half-cleared keypair.
func (b *Binding) DestroyMaterials() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.reclaimed {
		return ErrBindingReclaimed
	}
	if b.state != StateRetired && b.state != StateQuarantine {
		return ErrInvalidState
	}
	if b.asyncTasks != 0 {
		return ErrAsyncTaskActive
	}
	b.destroyMaterialsLocked()
	return nil
}

func (b *Binding) destroyMaterialsLocked() {
	if b.materialsWiped {
		return
	}
	var zeroKey PublicKey
	var zeroNonce Nonce
	var zeroResponse ResponseAuth
	var zeroCookie CookieAuth
	b.ephemeral = zeroKey
	b.nonce = zeroNonce
	b.response = zeroResponse
	b.cookie = zeroCookie
	b.hasCookie = false
	b.materialsWiped = true
}

func (b *Binding) transitionLocked(next LifecycleState, now time.Time, reason string) error {
	if b.reclaimed {
		return ErrBindingReclaimed
	}
	if !b.state.CanTransitionTo(next) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidState, b.state, next)
	}
	b.state = next
	b.stateChangedAt = now
	switch next {
	case StateResponseAccepted:
		b.acceptedAt = now
	case StateKeypairActive:
		b.activatedAt = now
	case StateRetired:
		b.retiredAt = now
		b.retireReason = reason
	case StateQuarantine:
		b.quarantinedAt = now
		b.quarantineReason = reason
	}
	return nil
}

func (b *Binding) matchesResponseLocked(msg HandshakeResponse) error {
	mac := msg.effectiveMAC()
	if subtleCompare(mac[:], b.response.MAC[:]) != 1 {
		return ErrMACMismatch
	}
	if subtleCompare(msg.Timestamp[:], b.response.Timestamp[:]) != 1 {
		return ErrTimestampMismatch
	}
	hash := msg.HandshakeHash
	if subtleCompare(hash[:], b.response.HandshakeHash[:]) != 1 {
		return ErrHandshakeHashMismatch
	}
	return nil
}

func (b *Binding) matchesCookieLocked(reply CookieReply, policy CookieNATPolicy, source netip.AddrPort) error {
	if !b.hasCookie {
		return ErrCookieExpectationMissing
	}
	if reply.Generation != 0 && reply.Generation != b.generation {
		return ErrGeneration
	}
	if b.cookie.Generation != 0 && reply.Generation != b.cookie.Generation {
		return ErrGeneration
	}
	if b.cookie.HandshakeHash != (HandshakeHash{}) &&
		(reply.HandshakeHash == (HandshakeHash{}) ||
			subtleCompare(reply.HandshakeHash[:], b.cookie.HandshakeHash[:]) != 1) {
		return ErrHandshakeHashMismatch
	}
	mac := reply.effectiveMAC()
	if subtleCompare(mac[:], b.cookie.MAC[:]) != 1 {
		return ErrCookieMACMismatch
	}
	if subtleCompare(reply.MessageDigest[:], b.cookie.MessageDigest[:]) != 1 {
		return ErrCookieDigestMismatch
	}

	want := normalizeAddrPort(b.cookie.Source)
	got := normalizeAddrPort(source)
	if !want.IsValid() {
		return ErrCookieSourceAddressMismatch
	}
	if !got.IsValid() {
		return ErrCookieSourceAddressMismatch
	}
	wantFamily := FamilyOf(want.Addr())
	gotFamily := FamilyOf(got.Addr())
	if wantFamily != gotFamily {
		return ErrCookieSourceFamilyMismatch
	}

	if policy == CookieNATAllowPortAndIPRebind {
		return nil
	}
	if want.Addr() != got.Addr() {
		return ErrCookieSourceAddressMismatch
	}
	if want.Port() != got.Port() && policy != CookieNATAllowPortRebind {
		return ErrCookieSourcePortMismatch
	}
	return nil
}

func (b *Binding) messageBoundReachedLocked() bool {
	if b.legacyMessageLimit == 0 && b.rejectAfterMessages == 0 {
		return true
	}
	if b.legacySealed {
		return true
	}
	if b.legacyMessageLimit > 0 && b.legacyMessages >= b.legacyMessageLimit {
		return true
	}
	if b.rejectAfterMessages > 0 && b.legacyMaxCounter >= b.rejectAfterMessages {
		return true
	}
	return false
}

func (b *Binding) noteLegacyMessageLocked(counter uint64) {
	if b.legacyMessages < ^uint64(0) {
		b.legacyMessages++
	}
	if counter > b.legacyMaxCounter {
		b.legacyMaxCounter = counter
	}
	if b.messageBoundReachedLocked() {
		b.legacySealed = true
	}
}

func deriveResponseAuth(authKey []byte, device DeviceID, peer PublicKey, generation uint64, ephemeral PublicKey, nonce Nonce, id BindingID, createdAt time.Time) ResponseAuth {
	write := func(h hashWriter) {
		writeLengthPrefixed(h, []byte("netsec/response-auth/v1"))
		writeLengthPrefixed(h, []byte(device))
		writeLengthPrefixed(h, peer[:])
		var gen [8]byte
		binary.BigEndian.PutUint64(gen[:], generation)
		writeLengthPrefixed(h, gen[:])
		writeLengthPrefixed(h, ephemeral[:])
		writeLengthPrefixed(h, nonce[:])
		writeLengthPrefixed(h, id[:])
		ts := TimestampFromTime(createdAt)
		writeLengthPrefixed(h, ts[:])
	}

	var sum []byte
	if len(authKey) > 0 {
		h := hmac.New(sha256.New, authKey)
		write(h)
		sum = h.Sum(nil)
	} else {
		h := sha256.New()
		write(h)
		sum = h.Sum(nil)
	}

	var out ResponseAuth
	copy(out.MAC[:], sum[:len(out.MAC)])
	copy(out.HandshakeHash[:], sum)
	out.Timestamp = TimestampFromTime(createdAt)
	return out
}

type hashWriter interface {
	Write([]byte) (int, error)
}

func subtleCompare(a, b []byte) int {
	return subtle.ConstantTimeCompare(a, b)
}
