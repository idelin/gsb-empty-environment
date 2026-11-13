package netsec

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

var (
	// ErrReleased is returned when a lease has already been released.
	ErrReleased = errors.New("netsec: cache lease already released")
	// ErrForeignLease means a lease belongs to a different cache.
	ErrForeignLease = errors.New("netsec: cache lease belongs to another cache")
	// ErrPublicScope means a public scope is unknown or does not allow the
	// requested token prefix.
	ErrPublicScope = errors.New("netsec: public scope does not allow this prefix")
	// ErrTenantMismatch means a private prefix cannot be extended by another
	// tenant.
	ErrTenantMismatch = errors.New("netsec: tenant does not own the prefix")
	// ErrNotPrefix means an extension request does not start with the parent's
	// exact canonical token prefix.
	ErrNotPrefix = errors.New("netsec: request does not extend the parent prefix")
	// ErrCoverage means per-layer K/V segments do not cover the token range
	// exactly once and without gaps.
	ErrCoverage = errors.New("netsec: KV segments do not cover the token range")
	// ErrBuilderDone means a builder has already committed or aborted.
	ErrBuilderDone = errors.New("netsec: builder already finished")
)

// Config configures a prefix cache.
type Config struct {
	// HostKey is the host's secret MAC key. It must be at least 32 bytes and is
	// never stored on entries.
	HostKey []byte
	// Pool is the block storage. If nil a pool with PoolBlocks slots is made.
	Pool *Pool
	// PoolBlocks is used when Pool is nil. Defaults to 1024.
	PoolBlocks int
	// MaxEntries bounds entries eligible for eviction. Entries with active
	// leases are never evicted. Defaults to 1024.
	MaxEntries int
	// PublicScopes registers the only token prefixes that may cross tenant
	// boundaries. An empty list disables public sharing entirely.
	PublicScopes []PublicScope
}

// LayerSegment is one immutable K/V block pair covering a token range for one
// transformer layer.
type LayerSegment struct {
	TokenStart int64
	TokenEnd   int64
	K          BlockHandle
	V          BlockHandle
}

// LayerKV is the ordered set of K/V segments for one layer.
type LayerKV struct {
	Layer    int
	Segments []LayerSegment
}

// SegmentData is the payload used to append a segment while building an entry.
type SegmentData struct {
	Layer      int
	TokenStart int64
	TokenEnd   int64
	K          []byte
	V          []byte
}

type entry struct {
	key          string
	canonical    []byte
	id           Identity
	rng          TokenRange
	promptDigest [32]byte
	layers       []LayerKV
	blockDigest  [32]byte
	mac          [32]byte
	generation   uint64
	refs         int
	retiring     bool
	released     bool
}

// Cache is a concurrency-safe shared-prefix KV cache.
type Cache struct {
	mu         sync.Mutex
	pool       *Pool
	macKey     []byte
	index      map[string]*entry
	public     map[string]map[[32]byte]struct{}
	maxEntries int
	generation uint64
}

// New creates a cache. HostKey must be supplied by the host; it is used to
// derive the content-MAC key.
func New(cfg Config) (*Cache, error) {
	if len(cfg.HostKey) < 32 {
		return nil, errors.New("netsec: host key must be at least 32 bytes")
	}
	pool := cfg.Pool
	if pool == nil {
		n := cfg.PoolBlocks
		if n == 0 {
			n = 1024
		}
		var err error
		pool, err = NewPool(n)
		if err != nil {
			return nil, err
		}
	}
	maxEntries := cfg.MaxEntries
	if maxEntries == 0 {
		maxEntries = 1024
	}
	if maxEntries < 1 {
		return nil, errors.New("netsec: MaxEntries must be positive")
	}
	public := make(map[string]map[[32]byte]struct{})
	for _, scope := range cfg.PublicScopes {
		if scope.Name == "" {
			return nil, errors.New("netsec: public scope name must not be empty")
		}
		if len(scope.TokenPrefixDigests) == 0 {
			return nil, fmt.Errorf("netsec: public scope %q has no allowed prefixes", scope.Name)
		}
		set := public[scope.Name]
		if set == nil {
			set = make(map[[32]byte]struct{})
			public[scope.Name] = set
		}
		for _, digest := range scope.TokenPrefixDigests {
			set[digest] = struct{}{}
		}
	}
	key := hmac.New(sha256.New, cfg.HostKey)
	_, _ = key.Write([]byte("netsec.kvprefix.mac-key.v1\x00"))
	macKey := key.Sum(nil)
	return &Cache{
		pool:       pool,
		macKey:     macKey,
		index:      make(map[string]*entry),
		public:     public,
		maxEntries: maxEntries,
	}, nil
}

// Pool returns the block pool used by the cache.
func (c *Cache) Pool() *Pool { return c.pool }

func (c *Cache) publicAllowed(scope string, digest [32]byte) bool {
	if scope == "" {
		return true
	}
	set := c.public[scope]
	if set == nil {
		return false
	}
	_, ok := set[digest]
	return ok
}

func (c *Cache) validateRequest(req Request) (Identity, TokenRange, error) {
	id, rng, err := req.validate()
	if err != nil {
		return Identity{}, TokenRange{}, err
	}
	if id.PublicScope != "" && !c.publicAllowed(id.PublicScope, id.TokenPrefixDigest) {
		return Identity{}, TokenRange{}, fmt.Errorf("%w: %q", ErrPublicScope, id.PublicScope)
	}
	return id, rng, nil
}

func (c *Cache) contentMAC(id Identity, rng TokenRange, blockDigest [32]byte) [32]byte {
	m := hmac.New(sha256.New, c.macKey)
	_, _ = m.Write([]byte("netsec.kvprefix.content-mac.v1\x00"))
	canonical := id.Canonical()
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], uint64(len(canonical)))
	_, _ = m.Write(lenBuf[:])
	_, _ = m.Write(canonical)
	binary.BigEndian.PutUint64(lenBuf[:], uint64(rng.Start))
	_, _ = m.Write(lenBuf[:])
	binary.BigEndian.PutUint64(lenBuf[:], uint64(rng.End))
	_, _ = m.Write(lenBuf[:])
	_, _ = m.Write(blockDigest[:])
	var out [32]byte
	m.Sum(out[:0])
	return out
}

// Lease is a reference to an immutable cached entry. Release must be called
// when the request finishes, including on cancellation or failure. Releasing
// the final lease scrubs the entry's blocks before returning them to the pool.
type Lease struct {
	cache    *Cache
	entry    *entry
	released atomic.Bool
}

// Identity returns a copy of the leased entry's identity.
func (l *Lease) Identity() Identity {
	if l == nil || l.entry == nil {
		return Identity{}
	}
	return l.entry.id
}

// Range returns the token range covered by the entry.
func (l *Lease) Range() TokenRange {
	if l == nil || l.entry == nil {
		return TokenRange{}
	}
	return l.entry.rng
}

// Generation returns the entry's creation generation.
func (l *Lease) Generation() uint64 {
	if l == nil || l.entry == nil {
		return 0
	}
	return l.entry.generation
}

// BlockDigest returns the digest of the K/V bytes covered by this entry.
func (l *Lease) BlockDigest() [32]byte {
	if l == nil || l.entry == nil {
		return [32]byte{}
	}
	return l.entry.blockDigest
}

// ContentMAC returns the host-key MAC binding the full identity, token range,
// and block digest. It is exposed for host-side audit logs, not as a substitute
// for verification.
func (l *Lease) ContentMAC() [32]byte {
	if l == nil || l.entry == nil {
		return [32]byte{}
	}
	return l.entry.mac
}

// Layers returns a deep copy of the per-layer block handles.
func (l *Lease) Layers() []LayerKV {
	if l == nil || l.entry == nil {
		return nil
	}
	out := make([]LayerKV, len(l.entry.layers))
	for i, layer := range l.entry.layers {
		out[i] = LayerKV{Layer: layer.Layer, Segments: append([]LayerSegment(nil), layer.Segments...)}
	}
	return out
}

// Released reports whether Release has been called.
func (l *Lease) Released() bool { return l == nil || l.released.Load() }

// Release drops this lease. It is idempotent. The last lease removes the entry
// from the index and scrubs all of its blocks.
func (l *Lease) Release() error {
	if l == nil || l.cache == nil {
		return nil
	}
	if !l.released.CompareAndSwap(false, true) {
		return nil
	}
	c := l.cache
	var handles []BlockHandle
	c.mu.Lock()
	e := l.entry
	if e == nil {
		c.mu.Unlock()
		return nil
	}
	if e.refs <= 0 {
		c.mu.Unlock()
		return errors.New("netsec: lease reference underflow")
	}
	e.refs--
	if e.refs == 0 {
		if c.index[e.key] == e {
			delete(c.index, e.key)
		}
		if !e.released {
			e.released = true
			handles = allBlockHandles(e.layers)
		}
	}
	c.mu.Unlock()
	releaseAll(c.pool, handles)
	return nil
}

func releaseAll(pool *Pool, handles []BlockHandle) {
	for _, h := range handles {
		_ = pool.Release(h)
	}
}

// verifyLocked checks identity, actual block contents, and the content MAC.
// The cache mutex must be held.
func (c *Cache) verifyLocked(e *entry) bool {
	if e.retiring {
		return false
	}
	digest, err := digestLayers(c.pool, e.layers)
	if err != nil || subtle.ConstantTimeCompare(digest[:], e.blockDigest[:]) != 1 {
		return false
	}
	got := c.contentMAC(e.id, e.rng, e.blockDigest)
	return hmac.Equal(got[:], e.mac[:])
}

// invalidateLocked removes an invalid entry from the index. It returns handles
// to release once the cache lock is dropped when there are no active leases.
func (c *Cache) invalidateLocked(e *entry) []BlockHandle {
	if c.index[e.key] == e {
		delete(c.index, e.key)
	}
	e.retiring = true
	if e.refs == 0 && !e.released {
		e.released = true
		return allBlockHandles(e.layers)
	}
	return nil
}

// Lookup returns a lease for an exact identity hit. A nil lease with ok=false
// is a normal miss. Identity mismatch, a bad MAC, or stale block generation
// are all misses; the caller never receives unverified blocks.
func (c *Cache) Lookup(ctx context.Context, req Request) (*Lease, bool, error) {
	id, _, err := c.validateRequest(req)
	if err != nil {
		return nil, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	key := string(id.Canonical())

	var invalid []BlockHandle
	c.mu.Lock()
	e := c.index[key]
	if e == nil || !bytes.Equal(e.canonical, []byte(key)) || !c.verifyLocked(e) {
		if e != nil {
			invalid = c.invalidateLocked(e)
		}
		c.mu.Unlock()
		releaseAll(c.pool, invalid)
		return nil, false, nil
	}
	// The entry itself owns one block reference. Holding the cache lock while
	// incrementing the lease count prevents a final release from scrubbing the
	// blocks before this lease exists.
	e.refs++
	lease := &Lease{cache: c, entry: e}
	c.mu.Unlock()

	if err := ctx.Err(); err != nil {
		_ = lease.Release()
		return nil, false, err
	}
	return lease, true, nil
}

// Builder accumulates immutable KV segments before publishing a new entry.
// Builders are not safe for concurrent use.
type Builder struct {
	cache       *Cache
	id          Identity
	rng         TokenRange
	prompt      [32]byte
	appendStart int64
	layout      int
	base        map[int][]LayerSegment
	added       map[int][]LayerSegment
	owned       []BlockHandle
	done        bool
}

// Begin starts building a new entry from scratch.
func (c *Cache) Begin(req Request) (*Builder, error) {
	id, rng, err := c.validateRequest(req)
	if err != nil {
		return nil, err
	}
	return &Builder{
		cache:       c,
		id:          id,
		rng:         rng,
		prompt:      req.PromptDigest,
		appendStart: rng.Start,
		layout:      int(id.KVLayoutVersion),
		base:        make(map[int][]LayerSegment),
		added:       make(map[int][]LayerSegment),
	}, nil
}

// BeginExtend starts a copy-on-write extension of parent. The parent entry is
// never modified. Prefix blocks are shared read-only; all newly appended
// private tokens must be supplied as new segments starting at the parent's end.
func (c *Cache) BeginExtend(parent *Lease, req Request) (*Builder, error) {
	if parent == nil || parent.Released() {
		return nil, ErrReleased
	}
	if parent.cache != c {
		return nil, ErrForeignLease
	}
	id, rng, err := c.validateRequest(req)
	if err != nil {
		return nil, err
	}
	var (
		parentID     Identity
		parentRange  TokenRange
		parentLayers []LayerKV
		parentPrompt [32]byte
	)
	c.mu.Lock()
	pe := parent.entry
	if pe == nil || pe.retiring || pe.refs <= 0 {
		c.mu.Unlock()
		return nil, ErrReleased
	}
	parentID = pe.id
	parentRange = pe.rng
	parentLayers = make([]LayerKV, len(pe.layers))
	for i, layer := range pe.layers {
		parentLayers[i] = LayerKV{Layer: layer.Layer, Segments: append([]LayerSegment(nil), layer.Segments...)}
	}
	parentPrompt = pe.promptDigest
	pinned := make([]BlockHandle, 0)
	for _, h := range allBlockHandles(parentLayers) {
		if err := c.pool.Pin(h); err != nil {
			for _, p := range pinned {
				_ = c.pool.Release(p)
			}
			c.mu.Unlock()
			return nil, err
		}
		pinned = append(pinned, h)
	}
	c.mu.Unlock()

	fail := func(err error) (*Builder, error) {
		releaseAll(c.pool, pinned)
		return nil, err
	}
	if id.ModelDigest != parentID.ModelDigest ||
		id.AdapterDigest != parentID.AdapterDigest ||
		id.TokenizerDigest != parentID.TokenizerDigest ||
		id.SafetyPolicyGeneration != parentID.SafetyPolicyGeneration ||
		id.KVLayoutVersion != parentID.KVLayoutVersion {
		return fail(errors.New("netsec: extension changes model, adapter, tokenizer, policy, or layout identity"))
	}
	if id.DecodePosition != parentID.DecodePosition || rng.Start != parentRange.Start {
		return fail(errors.New("netsec: extension changes decode position"))
	}
	if rng.End <= parentRange.End {
		return fail(errors.New("netsec: extension does not add tokens"))
	}
	if req.PromptDigest != parentPrompt {
		return fail(errors.New("netsec: extension changes the private system prompt"))
	}
	parentCount := parentRange.Length()
	if parentCount > int64(len(req.Tokens)) {
		return fail(ErrNotPrefix)
	}
	prefixDigest := CanonicalTokenPrefixDigest(req.Tokens[:parentCount], parentPrompt)
	if subtle.ConstantTimeCompare(prefixDigest[:], parentID.TokenPrefixDigest[:]) != 1 {
		return fail(ErrNotPrefix)
	}
	if parentID.PublicScope == "" {
		if id.PublicScope != "" {
			return fail(fmt.Errorf("%w: a private prefix cannot become public", ErrPublicScope))
		}
		if id.TenantID != parentID.TenantID {
			return fail(ErrTenantMismatch)
		}
	} else if id.PublicScope != "" && id.PublicScope != parentID.PublicScope {
		return fail(fmt.Errorf("%w: cannot move a prefix between public scopes", ErrPublicScope))
	}

	base := make(map[int][]LayerSegment, len(parentLayers))
	for _, layer := range parentLayers {
		base[layer.Layer] = append([]LayerSegment(nil), layer.Segments...)
	}
	return &Builder{
		cache:       c,
		id:          id,
		rng:         rng,
		prompt:      req.PromptDigest,
		appendStart: parentRange.End,
		layout:      int(id.KVLayoutVersion),
		base:        base,
		added:       make(map[int][]LayerSegment),
		owned:       pinned,
	}, nil
}

// AppendSegment allocates and fills one K/V block pair. For a scratch build,
// segments may cover any part of the request range. For an extension, segments
// must start at or after the parent's end; the suffix therefore always begins
// in new blocks.
func (b *Builder) AppendSegment(seg SegmentData) error {
	if b.done {
		return ErrBuilderDone
	}
	if seg.Layer < 0 || seg.Layer >= b.layout {
		return fmt.Errorf("netsec: layer %d outside KV layout version %d", seg.Layer, b.layout)
	}
	if seg.TokenStart < b.appendStart || seg.TokenEnd > b.rng.End || seg.TokenStart >= seg.TokenEnd {
		return fmt.Errorf("%w: segment [%d,%d), append start %d, request end %d",
			ErrCoverage, seg.TokenStart, seg.TokenEnd, b.appendStart, b.rng.End)
	}
	for _, existing := range b.added[seg.Layer] {
		if seg.TokenStart < existing.TokenEnd && existing.TokenStart < seg.TokenEnd {
			return fmt.Errorf("%w: overlapping segment on layer %d", ErrCoverage, seg.Layer)
		}
	}
	kh, err := b.allocate(seg.K)
	if err != nil {
		return err
	}
	vh, err := b.allocate(seg.V)
	if err != nil {
		_ = b.cache.pool.Release(kh)
		b.owned = removeHandle(b.owned, kh)
		return err
	}
	b.added[seg.Layer] = append(b.added[seg.Layer], LayerSegment{
		TokenStart: seg.TokenStart,
		TokenEnd:   seg.TokenEnd,
		K:          kh,
		V:          vh,
	})
	return nil
}

func removeHandle(handles []BlockHandle, h BlockHandle) []BlockHandle {
	for i := range handles {
		if handles[i] == h {
			return append(handles[:i], handles[i+1:]...)
		}
	}
	return handles
}

func (b *Builder) allocate(data []byte) (BlockHandle, error) {
	h, err := b.cache.pool.Allocate()
	if err != nil {
		return BlockHandle{}, err
	}
	b.owned = append(b.owned, h)
	if err := b.cache.pool.Fill(h, data); err != nil {
		b.owned = removeHandle(b.owned, h)
		_ = b.cache.pool.Release(h)
		return BlockHandle{}, err
	}
	return h, nil
}

func (b *Builder) combinedLayers() ([]LayerKV, error) {
	layers := make([]LayerKV, 0, b.layout)
	for layer := 0; layer < b.layout; layer++ {
		segments := append([]LayerSegment(nil), b.base[layer]...)
		segments = append(segments, b.added[layer]...)
		sort.Slice(segments, func(i, j int) bool {
			if segments[i].TokenStart == segments[j].TokenStart {
				return segments[i].TokenEnd < segments[j].TokenEnd
			}
			return segments[i].TokenStart < segments[j].TokenStart
		})
		if len(segments) == 0 {
			return nil, fmt.Errorf("%w: layer %d has no KV segments", ErrCoverage, layer)
		}
		expected := b.rng.Start
		for _, seg := range segments {
			if seg.TokenStart != expected || seg.TokenEnd <= seg.TokenStart {
				return nil, fmt.Errorf("%w: layer %d expected token %d, got [%d,%d)",
					ErrCoverage, layer, expected, seg.TokenStart, seg.TokenEnd)
			}
			expected = seg.TokenEnd
		}
		if expected != b.rng.End {
			return nil, fmt.Errorf("%w: layer %d ends at %d, want %d", ErrCoverage, layer, expected, b.rng.End)
		}
		layers = append(layers, LayerKV{Layer: layer, Segments: segments})
	}
	return layers, nil
}

// Commit validates coverage, authenticates the blocks, and publishes the entry.
// If an equivalent verified entry already exists, Commit returns a lease to it
// and releases the newly built blocks. If ctx is canceled the build aborts.
func (b *Builder) Commit(ctx context.Context) (*Lease, error) {
	if b.done {
		return nil, ErrBuilderDone
	}
	b.done = true
	if err := ctx.Err(); err != nil {
		b.releaseOwned()
		return nil, err
	}
	layers, err := b.combinedLayers()
	if err != nil {
		b.releaseOwned()
		return nil, err
	}
	blockDigest, err := digestLayers(b.cache.pool, layers)
	if err != nil {
		b.releaseOwned()
		return nil, err
	}
	mac := b.cache.contentMAC(b.id, b.rng, blockDigest)
	key := string(b.id.Canonical())

	var toRelease []BlockHandle
	c := b.cache
	c.mu.Lock()
	if err := ctx.Err(); err != nil {
		c.mu.Unlock()
		b.releaseOwned()
		return nil, err
	}
	if existing := c.index[key]; existing != nil {
		if c.verifyLocked(existing) {
			existing.refs++
			lease := &Lease{cache: c, entry: existing}
			c.mu.Unlock()
			b.releaseOwned()
			return lease, nil
		}
		toRelease = append(toRelease, c.invalidateLocked(existing)...)
	}
	c.generation++
	if c.generation == 0 {
		c.generation++
	}
	e := &entry{
		key:          key,
		canonical:    []byte(key),
		id:           b.id,
		rng:          b.rng,
		promptDigest: b.prompt,
		layers:       layers,
		blockDigest:  blockDigest,
		mac:          mac,
		generation:   c.generation,
		refs:         1,
	}
	c.index[key] = e
	toRelease = append(toRelease, c.evictLocked(e)...)
	lease := &Lease{cache: c, entry: e}
	c.mu.Unlock()

	// The newly allocated blocks and any parent pins are now owned by the
	// published entry. They must not be released by this builder.
	b.owned = nil
	releaseAll(c.pool, toRelease)
	return lease, nil
}

func (c *Cache) evictLocked(except *entry) []BlockHandle {
	var out []BlockHandle
	for len(c.index) > c.maxEntries {
		var victim *entry
		for _, e := range c.index {
			if e == except || e.refs != 0 || e.retiring {
				continue
			}
			victim = e
			break
		}
		if victim == nil {
			break
		}
		out = append(out, c.invalidateLocked(victim)...)
	}
	return out
}

func (b *Builder) releaseOwned() {
	handles := b.owned
	b.owned = nil
	releaseAll(b.cache.pool, handles)
}

// Abort discards the build, returns all reserved blocks, and scrubs them.
func (b *Builder) Abort() {
	if b == nil || b.done {
		return
	}
	b.done = true
	b.releaseOwned()
}

// Create builds a complete entry from scratch.
func (c *Cache) Create(ctx context.Context, req Request, segments []SegmentData) (*Lease, error) {
	if lease, ok, err := c.Lookup(ctx, req); err != nil || ok {
		return lease, err
	}
	b, err := c.Begin(req)
	if err != nil {
		return nil, err
	}
	for _, seg := range segments {
		if err := b.AppendSegment(seg); err != nil {
			b.Abort()
			return nil, err
		}
	}
	return b.Commit(ctx)
}

// Extend builds a copy-on-write child of parent. The segment payloads must
// cover exactly the newly appended token suffix on every layer.
func (c *Cache) Extend(ctx context.Context, parent *Lease, req Request, suffix []SegmentData) (*Lease, error) {
	b, err := c.BeginExtend(parent, req)
	if err != nil {
		return nil, err
	}
	for _, seg := range suffix {
		if err := b.AppendSegment(seg); err != nil {
			b.Abort()
			return nil, err
		}
	}
	return b.Commit(ctx)
}

// digestLayers hashes K/V bytes in canonical layer and token order.
func digestLayers(pool *Pool, layers []LayerKV) ([32]byte, error) {
	ordered := append([]LayerKV(nil), layers...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Layer < ordered[j].Layer })
	h := sha256.New()
	_, _ = h.Write([]byte("netsec.kvprefix.layers.v1\x00"))
	var buf [8]byte
	binary.BigEndian.PutUint32(buf[:4], uint32(len(ordered)))
	_, _ = h.Write(buf[:4])
	for _, layer := range ordered {
		segments := append([]LayerSegment(nil), layer.Segments...)
		sort.Slice(segments, func(i, j int) bool {
			if segments[i].TokenStart == segments[j].TokenStart {
				return segments[i].TokenEnd < segments[j].TokenEnd
			}
			return segments[i].TokenStart < segments[j].TokenStart
		})
		binary.BigEndian.PutUint32(buf[:4], uint32(layer.Layer))
		_, _ = h.Write(buf[:4])
		binary.BigEndian.PutUint32(buf[:4], uint32(len(segments)))
		_, _ = h.Write(buf[:4])
		for _, seg := range segments {
			putUint64(buf[:], uint64(seg.TokenStart))
			_, _ = h.Write(buf[:])
			putUint64(buf[:], uint64(seg.TokenEnd))
			_, _ = h.Write(buf[:])
			for _, handle := range []BlockHandle{seg.K, seg.V} {
				data, err := pool.Read(handle)
				if err != nil {
					return [32]byte{}, err
				}
				putUint64(buf[:], uint64(len(data)))
				_, _ = h.Write(buf[:])
				_, _ = h.Write(data)
			}
		}
	}
	var out [32]byte
	h.Sum(out[:0])
	return out, nil
}
