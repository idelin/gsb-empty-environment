package netsec

import (
	"encoding/binary"
	"errors"
	"sync"
)

var (
	// errEmptyRoot is returned when a host is created with an empty root key.
	errEmptyRoot = errors.New("netsec: root key must not be empty")
	// errRootRevoked is returned when derivation or rotation is attempted on
	// a revoked host.
	errRootRevoked = errors.New("netsec: root key has been revoked")
	// errHandleSpent is returned when a one-time derived handle is used more
	// than once.
	errHandleSpent = errors.New("netsec: derived handle has already been consumed")
	// errNilUse is returned when a handle is invoked without a callback.
	errNilUse = errors.New("netsec: handle use callback must not be nil")
)

// CryptoOp identifies which primitive an observed cryptographic event used.
type CryptoOp string

const (
	// CryptoExtract marks an HKDF-Extract invocation.
	CryptoExtract CryptoOp = "hkdf-extract"
	// CryptoExpand marks an HKDF-Expand invocation.
	CryptoExpand CryptoOp = "hkdf-expand"
)

// CryptoEvent is delivered to the registered observer for every
// cryptographic operation the host performs. Events are only emitted for
// valid contexts: rejected contexts never reach the cryptographic layer.
type CryptoEvent struct {
	// Op is the primitive that ran.
	Op CryptoOp
	// Generation is the root generation that produced the key material.
	Generation uint64
	// Context is a copy of the validated context of the derivation.
	Context Context
}

// CryptoObserver receives cryptographic operation events. Implementations
// must be safe for concurrent use because derivation may run concurrently.
type CryptoObserver func(CryptoEvent)

// Host owns a root key and is the only object able to derive keys. The root
// key itself is never exported: callers only ever receive one-time Handle
// values that hand the derived key to a callback inside the host boundary.
type Host struct {
	mu         sync.RWMutex
	root       []byte
	generation uint64
	revoked    bool
	// cache maps (root generation, full context) to derived key material, so
	// repeated derivation of the same context under the same root reuses the
	// result while distinct generations and contexts never share entries.
	cache    map[string][]byte
	observer CryptoObserver
}

// Option configures a Host at construction time.
type Option func(*Host)

// WithCryptoObserver registers an observer for every cryptographic operation
// the host performs. It is intended for tests that need to assert which
// contexts actually reached the cryptographic layer.
func WithCryptoObserver(observer CryptoObserver) Option {
	return func(h *Host) {
		h.observer = observer
	}
}

// NewHost creates a host around rootKey. The root key is copied so later
// mutation of the caller's slice cannot affect the host. The first root
// starts at generation 1.
func NewHost(rootKey []byte, opts ...Option) (*Host, error) {
	if len(rootKey) == 0 {
		return nil, errEmptyRoot
	}
	host := &Host{
		root:       append([]byte(nil), rootKey...),
		generation: 1,
		cache:      make(map[string][]byte),
	}
	for _, opt := range opts {
		opt(host)
	}
	return host, nil
}

// Generation returns the current root generation. Rotating the root advances
// it; revocation does not.
func (h *Host) Generation() uint64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.generation
}

// RotateRoot replaces the root key and advances the generation. Cached
// material derived under earlier generations is retained but can only be
// reached through handles already carrying those generations; new derivation
// always uses the newest root. Rotation of a revoked host fails.
func (h *Host) RotateRoot(newRoot []byte) error {
	if len(newRoot) == 0 {
		return errEmptyRoot
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.revoked {
		return errRootRevoked
	}
	zeroize(h.root)
	h.root = append([]byte(nil), newRoot...)
	h.generation++
	return nil
}

// Revoke invalidates the root. After revocation, Derive and RotateRoot always
// fail without performing any cryptographic operation, and cached material is
// destroyed. Handles already handed out remain usable for their single use.
func (h *Host) Revoke() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.revoked {
		return
	}
	h.revoked = true
	zeroize(h.root)
	for key, material := range h.cache {
		zeroize(material)
		delete(h.cache, key)
	}
}

// derivationCacheKey builds a key from the root generation and an already
// encoded canonical context. Because the context encoding is injective, two
// distinct contexts can never map to the same key.
func derivationCacheKey(generation uint64, info []byte) string {
	var genPrefix [8]byte
	binary.BigEndian.PutUint64(genPrefix[:], generation)
	key := make([]byte, 0, 8+len(info))
	key = append(key, genPrefix[:]...)
	key = append(key, info...)
	return string(key)
}

// Derive returns a one-time handle for key material derived under the current
// root and ctx. Validation runs before any cryptography, so invalid contexts
// produce an error and zero observed cryptographic events. Derivation is
// serialized against revocation: a concurrent Revoke can only win either
// before the whole derivation (which then fails) or after it (which still
// yields a valid one-time handle).
func (h *Host) Derive(ctx Context, length int) (*Handle, error) {
	if err := ctx.validate(); err != nil {
		return nil, err
	}
	if length < 0 || length > 255*sha256Size {
		return nil, errHKDFLength
	}
	info := ctx.canonicalInfo()

	h.mu.Lock()
	if h.revoked {
		h.mu.Unlock()
		return nil, errRootRevoked
	}
	generation := h.generation
	key := derivationCacheKey(generation, info)
	material, ok := h.cache[key]
	if !ok {
		// RFC 5869 with a nil salt: Extract folds in an all-zero salt and the
		// full canonical context is bound through the info parameter.
		prk := Extract(nil, h.root)
		derived, err := Expand(prk, info, length)
		if err != nil {
			h.mu.Unlock()
			return nil, err
		}
		h.cache[key] = derived
		material = derived
	}
	handle := &Handle{
		key:        append([]byte(nil), material...),
		generation: generation,
		context:    cloneContext(ctx),
	}
	observer := h.observer
	h.mu.Unlock()

	// Fresh extraction/expansion emit both events; a cache hit performs no
	// cryptographic operation at all.
	if !ok && observer != nil {
		observer(CryptoEvent{Op: CryptoExtract, Generation: generation, Context: cloneContext(ctx)})
		observer(CryptoEvent{Op: CryptoExpand, Generation: generation, Context: cloneContext(ctx)})
	}
	return handle, nil
}

// Handle is a single-use, scoped view of derived key material. The raw key
// never leaves the host: it is passed to the Use callback and zeroized as
// soon as the callback returns.
type Handle struct {
	mu         sync.Mutex
	key        []byte
	generation uint64
	context    Context
	spent      bool
}

// Generation reports the root generation the material was derived under.
func (h *Handle) Generation() uint64 {
	return h.generation
}

// Context returns a copy of the context bound to the handle.
func (h *Handle) Context() Context {
	return cloneContext(h.context)
}

// Use runs fn with the derived key exactly once. Subsequent calls, including
// concurrent ones, fail with errHandleSpent without invoking fn. The key
// buffer is zeroized after the first call regardless of fn's outcome.
func (h *Handle) Use(fn func(key []byte) error) error {
	if fn == nil {
		return errNilUse
	}
	h.mu.Lock()
	if h.spent {
		h.mu.Unlock()
		return errHandleSpent
	}
	h.spent = true
	key := h.key
	h.mu.Unlock()

	defer zeroize(key)
	return fn(key)
}

// cloneContext returns a deep copy of ctx so callers cannot mutate slices
// shared with the host or an observed event.
func cloneContext(ctx Context) Context {
	return Context{
		Tenant:          append([]byte(nil), ctx.Tenant...),
		Purpose:         ctx.Purpose,
		ProtocolVersion: append([]byte(nil), ctx.ProtocolVersion...),
		Direction:       ctx.Direction,
		Epoch:           ctx.Epoch,
	}
}

// zeroize overwrites b with zeroes so discarded key material does not linger
// in memory.
func zeroize(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
