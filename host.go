package netsec

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

const (
	// MaxRootKeyBytes bounds imported root key material.
	MaxRootKeyBytes = 4096

	defaultHKDFSalt = "example.org/netsec/q-022/hkdf-sha256/v1"
)

var (
	ErrInvalidHost         = errors.New("netsec: invalid host")
	ErrInvalidRoot         = errors.New("netsec: invalid root handle")
	ErrRootRevoked         = errors.New("netsec: root has been revoked")
	ErrInvalidRootKey      = errors.New("netsec: invalid root key")
	ErrDuplicateGeneration = errors.New("netsec: root generation already active")
	ErrGenerationReused    = errors.New("netsec: rotation must change the root generation")
	ErrInvalidKeyLength    = errors.New("netsec: invalid derived key length")
	ErrInvalidHandle       = errors.New("netsec: invalid derived key handle")
	ErrHandleUsed          = errors.New("netsec: derived key handle already used")
	ErrNilOperation        = errors.New("netsec: nil key operation")
)

// CryptoOperation identifies an observable cryptographic step. CacheHit and Use
// are not themselves key derivations, but are reported so callers can audit the
// full lifecycle without seeing key material.
type CryptoOperation uint8

const (
	CryptoExtract CryptoOperation = iota + 1
	CryptoExpand
	CryptoCacheHit
	CryptoUse
)

func (op CryptoOperation) String() string {
	switch op {
	case CryptoExtract:
		return "extract"
	case CryptoExpand:
		return "expand"
	case CryptoCacheHit:
		return "cache-hit"
	case CryptoUse:
		return "use"
	default:
		return fmt.Sprintf("crypto-operation(%d)", uint8(op))
	}
}

// CryptoEvent is emitted for every host-observed cryptographic operation. It
// deliberately contains metadata only: tenant bytes and key material are never
// included.
type CryptoEvent struct {
	Operation      CryptoOperation
	RootID         uint64
	RootGeneration uint64
	Purpose        Purpose
	Direction      Direction
	Version        uint16
	Epoch          uint64
	KeyLength      int
	CacheHit       bool
}

// Observer receives host cryptographic operation events. Observers must not
// retain or attempt to recover key material; none is supplied.
type Observer func(CryptoEvent)

// Option configures a Host.
type Option func(*Host)

// WithObserver installs an operation observer.
func WithObserver(observer Observer) Option {
	return func(h *Host) {
		h.observer = observer
	}
}

// Host is the trusted key authority. It owns root key material and derived key
// handles. Plugins only ever receive opaque handles.
type Host struct {
	mu         sync.Mutex
	roots      map[uint64]*rootEntry
	nextRootID uint64
	cache      map[deriveCacheKey][]byte
	salt       []byte

	observerMu sync.RWMutex
	observer   Observer
}

type rootEntry struct {
	id         uint64
	generation uint64
	key        []byte
	revoked    bool
}

type deriveCacheKey struct {
	rootID     uint64
	generation uint64
	context    string
	length     int
}

// RootHandle is an opaque capability for one root key. It contains no key
// bytes; the Host remains the sole owner of the root material.
type RootHandle struct {
	host       *Host
	id         uint64
	generation uint64
}

// Generation returns the generation bound to the handle.
func (rh RootHandle) Generation() uint64 {
	return rh.generation
}

// IsZero reports whether the handle is the invalid zero value.
func (rh RootHandle) IsZero() bool {
	return rh.host == nil || rh.id == 0
}

// NewHost constructs a key authority with no roots installed.
func NewHost(options ...Option) *Host {
	h := &Host{
		roots:      make(map[uint64]*rootEntry),
		nextRootID: 1,
		cache:      make(map[deriveCacheKey][]byte),
		salt:       []byte(defaultHKDFSalt),
	}
	for _, option := range options {
		if option != nil {
			option(h)
		}
	}
	return h
}

// SetObserver replaces the operation observer. A nil observer disables
// reporting.
func (h *Host) SetObserver(observer Observer) {
	if h == nil {
		return
	}
	h.observerMu.Lock()
	h.observer = observer
	h.observerMu.Unlock()
}

// InstallRoot imports root key material and returns an opaque handle. The
// caller retains ownership of key; the host keeps its own copy.
func (h *Host) InstallRoot(generation uint64, key []byte) (RootHandle, error) {
	if h == nil {
		return RootHandle{}, ErrInvalidHost
	}
	if len(key) == 0 || len(key) > MaxRootKeyBytes {
		return RootHandle{}, fmt.Errorf("%w: need 1..%d bytes", ErrInvalidRootKey, MaxRootKeyBytes)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.initLocked()

	for _, entry := range h.roots {
		if !entry.revoked && entry.generation == generation {
			return RootHandle{}, fmt.Errorf("%w: %d", ErrDuplicateGeneration, generation)
		}
	}

	id := h.nextRootID
	h.nextRootID++
	entry := &rootEntry{
		id:         id,
		generation: generation,
		key:        append([]byte(nil), key...),
	}
	h.roots[id] = entry
	return RootHandle{host: h, id: id, generation: generation}, nil
}

// GenerateRoot creates fresh random root key material inside the host and
// installs it without ever exposing the bytes.
func (h *Host) GenerateRoot(generation uint64) (RootHandle, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return RootHandle{}, fmt.Errorf("netsec: generate root: %w", err)
	}
	defer clear(key)
	return h.InstallRoot(generation, key)
}

// RotateRoot atomically installs a new root and revokes old. The old
// generation's derived-key cache is erased. Handles derived before rotation
// remain one-shot capabilities, as with any other revocation.
func (h *Host) RotateRoot(old RootHandle, generation uint64, key []byte) (RootHandle, error) {
	if h == nil {
		return RootHandle{}, ErrInvalidHost
	}
	if len(key) == 0 || len(key) > MaxRootKeyBytes {
		return RootHandle{}, fmt.Errorf("%w: need 1..%d bytes", ErrInvalidRootKey, MaxRootKeyBytes)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	h.initLocked()

	oldEntry, err := h.rootLocked(old)
	if err != nil {
		return RootHandle{}, err
	}
	if oldEntry.revoked {
		return RootHandle{}, ErrRootRevoked
	}
	if oldEntry.generation == generation {
		return RootHandle{}, fmt.Errorf("%w: %d", ErrGenerationReused, generation)
	}
	for _, entry := range h.roots {
		if entry.id != oldEntry.id && !entry.revoked && entry.generation == generation {
			return RootHandle{}, fmt.Errorf("%w: %d", ErrDuplicateGeneration, generation)
		}
	}

	id := h.nextRootID
	h.nextRootID++
	newEntry := &rootEntry{
		id:         id,
		generation: generation,
		key:        append([]byte(nil), key...),
	}
	h.roots[id] = newEntry
	h.revokeLocked(oldEntry)

	return RootHandle{host: h, id: id, generation: generation}, nil
}

// RevokeRoot revokes a root, erases its key and derived-key cache, and makes
// future Derive calls for the root fail. It is idempotent.
func (h *Host) RevokeRoot(root RootHandle) error {
	if h == nil {
		return ErrInvalidHost
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	entry, err := h.rootLocked(root)
	if err != nil {
		return err
	}
	if entry.revoked {
		return nil
	}
	h.revokeLocked(entry)
	return nil
}

// RootActive reports whether the root is installed and not revoked.
func (h *Host) RootActive(root RootHandle) bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	entry, err := h.rootLocked(root)
	return err == nil && !entry.revoked
}

// Close revokes all roots and clears the derived-key cache.
func (h *Host) Close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, entry := range h.roots {
		if !entry.revoked {
			h.revokeLocked(entry)
		}
	}
	for key, value := range h.cache {
		clear(value)
		delete(h.cache, key)
	}
}

// Derive creates a one-shot derived key handle for a root and application
// context. HKDF-SHA256 info is a length-prefixed root generation followed by
// the canonical context encoding. The context is validated and encoded before
// the root is touched, so malformed contexts cannot cause Extract or Expand
// operations.
func (h *Host) Derive(root RootHandle, context Context, keyLength int) (*DerivedHandle, error) {
	if h == nil {
		return nil, ErrInvalidHost
	}
	encoded, err := context.Encode()
	if err != nil {
		return nil, err
	}
	if keyLength <= 0 || keyLength > MaxExpandLength {
		return nil, fmt.Errorf("%w: need 1..%d bytes", ErrInvalidKeyLength, MaxExpandLength)
	}

	h.mu.Lock()
	h.initLocked()
	entry, err := h.rootLocked(root)
	if err != nil {
		h.mu.Unlock()
		return nil, err
	}
	if entry.revoked {
		h.mu.Unlock()
		return nil, ErrRootRevoked
	}

	cacheKey := deriveCacheKey{
		rootID:     entry.id,
		generation: entry.generation,
		context:    string(encoded),
		length:     keyLength,
	}

	var (
		material []byte
		events   []CryptoEvent
	)
	if cached, ok := h.cache[cacheKey]; ok {
		material = append([]byte(nil), cached...)
		events = append(events, h.event(entry, context, keyLength, CryptoCacheHit, true))
	} else {
		prk := Extract(h.salt, entry.key)
		okm, expandErr := Expand(prk, derivationInfo(entry.generation, encoded), keyLength)
		clear(prk)
		if expandErr != nil {
			h.mu.Unlock()
			return nil, expandErr
		}

		h.cache[cacheKey] = okm
		material = append([]byte(nil), okm...)
		events = append(events,
			h.event(entry, context, keyLength, CryptoExtract, false),
			h.event(entry, context, keyLength, CryptoExpand, false),
		)
	}

	handle := &DerivedHandle{
		host:       h,
		generation: entry.generation,
		context:    cloneContext(context),
		state: &derivedState{
			key:  material,
			size: len(material),
		},
	}
	h.mu.Unlock()

	for _, event := range events {
		h.emit(event)
	}
	return handle, nil
}

// DeriveKey is an alias for Derive.
func (h *Host) DeriveKey(root RootHandle, context Context, keyLength int) (*DerivedHandle, error) {
	return h.Derive(root, context, keyLength)
}

// Key is a scoped view of derived key material. It intentionally exposes no
// method that returns the underlying bytes.
type Key struct {
	material []byte
}

// Size returns the derived key length in bytes.
func (k *Key) Size() int {
	if k == nil {
		return 0
	}
	return len(k.material)
}

// HMAC computes HMAC-SHA256 using the derived key.
func (k *Key) HMAC(message []byte) []byte {
	if k == nil || len(k.material) == 0 {
		return nil
	}
	mac := hmac.New(sha256.New, k.material)
	_, _ = mac.Write(message)
	return mac.Sum(nil)
}

// HMACSHA256 is an explicit alias for HMAC.
func (k *Key) HMACSHA256(message []byte) []byte {
	return k.HMAC(message)
}

type derivedState struct {
	key  []byte
	size int
	used atomic.Bool
}

// DerivedHandle is an opaque, one-shot capability for a derived key. Copies of
// the handle refer to the same one-shot state.
type DerivedHandle struct {
	host       *Host
	generation uint64
	context    Context
	state      *derivedState
}

// RootGeneration returns the generation of the root that authorized the key.
func (dh *DerivedHandle) RootGeneration() uint64 {
	if dh == nil {
		return 0
	}
	return dh.generation
}

// Context returns a copy of the derivation context.
func (dh *DerivedHandle) Context() Context {
	if dh == nil {
		return Context{}
	}
	return cloneContext(dh.context)
}

// KeySize returns the derived key length.
func (dh *DerivedHandle) KeySize() int {
	if dh == nil || dh.state == nil {
		return 0
	}
	return dh.state.size
}

// Used reports whether the one-shot handle has been consumed.
func (dh *DerivedHandle) Used() bool {
	if dh == nil || dh.state == nil {
		return true
	}
	return dh.state.used.Load()
}

// Use consumes the handle and invokes operation with a scoped Key view. The key
// material is zeroed when operation returns, whether it succeeds or fails. A
// handle can be consumed exactly once, including across copies and concurrent
// callers. Root revocation does not invalidate a handle that was already
// issued.
func (dh *DerivedHandle) Use(operation func(*Key) error) error {
	if operation == nil {
		return ErrNilOperation
	}
	if dh == nil || dh.host == nil || dh.state == nil {
		return ErrInvalidHandle
	}
	if !dh.state.used.CompareAndSwap(false, true) {
		return ErrHandleUsed
	}

	key := &Key{material: dh.state.key}
	defer func() {
		clear(key.material)
		dh.state.key = nil
	}()

	err := operation(key)
	dh.host.emit(CryptoEvent{
		Operation:      CryptoUse,
		RootGeneration: dh.generation,
		Purpose:        dh.context.Purpose,
		Direction:      dh.context.Direction,
		Version:        dh.context.Version,
		Epoch:          dh.context.Epoch,
		KeyLength:      dh.state.size,
	})
	return err
}

// WithKey is an alias for Use.
func (dh *DerivedHandle) WithKey(operation func(*Key) error) error {
	return dh.Use(operation)
}

func (h *Host) rootLocked(root RootHandle) (*rootEntry, error) {
	if root.host != h || root.id == 0 {
		return nil, ErrInvalidRoot
	}
	entry, ok := h.roots[root.id]
	if !ok || entry.generation != root.generation {
		return nil, ErrInvalidRoot
	}
	return entry, nil
}

// derivationInfo binds a derivation to a root generation before the canonical
// application context. The generation is length-prefixed like every context
// field, so rotating a root cannot silently reuse a key even if the same root
// bytes were imported under a new generation.
func derivationInfo(generation uint64, encodedContext []byte) []byte {
	var generationBytes [8]byte
	binary.BigEndian.PutUint64(generationBytes[:], generation)
	info := make([]byte, 0, 4+len(generationBytes)+len(encodedContext))
	info = appendField(info, generationBytes[:])
	return append(info, encodedContext...)
}

func (h *Host) initLocked() {
	if h.roots == nil {
		h.roots = make(map[uint64]*rootEntry)
	}
	if h.cache == nil {
		h.cache = make(map[deriveCacheKey][]byte)
	}
	if h.nextRootID == 0 {
		h.nextRootID = 1
	}
	if h.salt == nil {
		h.salt = []byte(defaultHKDFSalt)
	}
}

func (h *Host) revokeLocked(entry *rootEntry) {
	entry.revoked = true
	clear(entry.key)
	entry.key = nil
	for key, value := range h.cache {
		if key.rootID == entry.id {
			clear(value)
			delete(h.cache, key)
		}
	}
}

func (h *Host) event(entry *rootEntry, context Context, keyLength int, operation CryptoOperation, cacheHit bool) CryptoEvent {
	return CryptoEvent{
		Operation:      operation,
		RootID:         entry.id,
		RootGeneration: entry.generation,
		Purpose:        context.Purpose,
		Direction:      context.Direction,
		Version:        context.Version,
		Epoch:          context.Epoch,
		KeyLength:      keyLength,
		CacheHit:       cacheHit,
	}
}

func (h *Host) emit(event CryptoEvent) {
	h.observerMu.RLock()
	observer := h.observer
	h.observerMu.RUnlock()
	if observer != nil {
		observer(event)
	}
}

func cloneContext(context Context) Context {
	context.Tenant = append([]byte(nil), context.Tenant...)
	return context
}
