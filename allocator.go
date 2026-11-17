package netsec

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"
)

const (
	// DefaultInitialCodeLength is the default number of base32 characters in a
	// newly allocated short code. Each character contributes five bits.
	DefaultInitialCodeLength = 12

	// MinKeySize is the minimum HMAC key length accepted by the built-in
	// HMAC-SHA-256 MAC.
	MinKeySize = 16

	maxInitialCodeLength = 64
	snapshotVersion      = 1
	macDomain            = "netsec/pseudonym/v1\x00"
	keyCheckDomain       = "netsec/key-check/v1\x00"
)

var (
	// ErrInvalidOptions reports an unusable allocator configuration.
	ErrInvalidOptions = errors.New("netsec: invalid options")

	// ErrInvalidIdentifier reports an empty identifier.
	ErrInvalidIdentifier = errors.New("netsec: invalid identifier")

	// ErrCodeSpaceExhausted reports that no unused prefix remains. This means
	// the MAC output is too short for the requested code length.
	ErrCodeSpaceExhausted = errors.New("netsec: short-code space exhausted")

	// ErrUnknownCode reports that a short code is not present in the index.
	ErrUnknownCode = errors.New("netsec: unknown short code")

	// ErrIdentityMismatch reports that the identifier's full MAC does not own
	// the supplied short code, even though the code exists in the index.
	ErrIdentityMismatch = errors.New("netsec: identifier does not own short code")

	// ErrCorruptStore reports a malformed or internally inconsistent snapshot.
	ErrCorruptStore = errors.New("netsec: corrupt store")

	// ErrIncompatibleStore reports a snapshot written with a different key or
	// initial code length. Opening it with the wrong key would silently create
	// new identities for existing people.
	ErrIncompatibleStore = errors.New("netsec: incompatible store")
)

var base32NoPadding = base32.StdEncoding.WithPadding(base32.NoPadding)

// MACFunc computes a keyed digest for an identifier. It is injectable so tests
// can deliberately produce colliding prefixes. Production callers should leave
// it nil and use Key with the built-in HMAC-SHA-256 implementation.
//
// A custom MACFunc may be called concurrently.
type MACFunc func(identifier []byte) []byte

// Options configures an Allocator.
type Options struct {
	// Key is the HMAC key. It must be at least MinKeySize bytes when MAC is nil.
	// The allocator copies it.
	Key []byte

	// Store persists snapshots. If nil, an in-memory store is used and the
	// allocator does not survive process restart.
	Store Store

	// InitialCodeLength is the number of base32 characters in the first code
	// allocated for an identity. Zero selects DefaultInitialCodeLength.
	InitialCodeLength int

	// MAC replaces the built-in HMAC-SHA-256 function. This is intended for
	// deterministic collision and recovery tests; do not use it in production.
	MAC MACFunc
}

// Allocator assigns and resolves pseudonymous short codes.
//
// All methods are safe for concurrent use. Allocation holds a mutex across the
// index update and the durable save, so an allocation is either fully
// committed or fully rolled back.
type Allocator struct {
	mu sync.Mutex

	mac           MACFunc
	keyID         string
	initialLength int
	store         Store
	state         *state
}

type record struct {
	Identity string
	Code     string
	Length   int
	Aliases  []string
}

type state struct {
	records   map[string]*record
	canonical map[string]string
	index     map[string]map[string]struct{}
}

type snapshot struct {
	Version           int             `json:"version"`
	KeyID             string          `json:"key_id,omitempty"`
	InitialCodeLength int             `json:"initial_code_length"`
	Entries           []snapshotEntry `json:"entries"`
}

type snapshotEntry struct {
	Identity string   `json:"hmac"`
	Code     string   `json:"short_code"`
	Length   int      `json:"length"`
	Aliases  []string `json:"aliases,omitempty"`
}

// NewAllocator loads or creates an allocator.
func NewAllocator(opts Options) (*Allocator, error) {
	return NewAllocatorContext(context.Background(), opts)
}

// NewAllocatorContext is NewAllocator with a context controlling the initial
// store load.
func NewAllocatorContext(ctx context.Context, opts Options) (*Allocator, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	initialLength := opts.InitialCodeLength
	if initialLength == 0 {
		initialLength = DefaultInitialCodeLength
	}
	if initialLength < 1 || initialLength > maxInitialCodeLength {
		return nil, fmt.Errorf("%w: initial code length must be in [1,%d]", ErrInvalidOptions, maxInitialCodeLength)
	}

	key := slices.Clone(opts.Key)
	mac := opts.MAC
	if mac == nil {
		if len(key) < MinKeySize {
			return nil, fmt.Errorf("%w: key must be at least %d bytes", ErrInvalidOptions, MinKeySize)
		}
		mac = newHMAC(key)
	}

	store := opts.Store
	if store == nil {
		store = NewMemoryStore()
	}

	a := &Allocator{
		mac:           mac,
		initialLength: initialLength,
		store:         store,
		state:         newState(),
	}
	if len(key) != 0 {
		a.keyID = keyCheckValue(key)
	}

	raw, err := store.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("netsec: load store: %w", err)
	}
	if len(raw) == 0 {
		return a, nil
	}

	st, err := a.decodeSnapshot(raw)
	if err != nil {
		return nil, err
	}
	a.state = st
	return a, nil
}

// Assign returns the stable short code for identifier.
//
// The first caller for a new identity receives the shortest available prefix.
// If that prefix is already owned, the new identity receives a longer prefix;
// codes that were already returned for other identities are never modified.
// Reassigning an existing identifier performs no write and returns its original
// code.
func (a *Allocator) Assign(ctx context.Context, identifier string) (string, error) {
	ctx = nonNilContext(ctx)
	if identifier == "" {
		return "", ErrInvalidIdentifier
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	digest := slices.Clone(a.mac([]byte(identifier)))
	if len(digest) == 0 {
		return "", fmt.Errorf("%w: MAC returned an empty digest", ErrInvalidOptions)
	}
	identity := hex.EncodeToString(digest)
	encoded := encodeDigest(digest)
	if len(encoded) < a.initialLength {
		return "", fmt.Errorf("%w: %d-byte digest encodes to %d characters, need %d",
			ErrCodeSpaceExhausted, len(digest), len(encoded), a.initialLength)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return "", err
	}
	if existing, ok := a.state.records[identity]; ok {
		return existing.Code, nil
	}

	next := a.state.clone()
	assigned, err := next.allocate(identity, encoded, a.initialLength)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := a.save(ctx, next); err != nil {
		return "", err
	}
	a.state = next
	return assigned.Code, nil
}

// Resolve confirms that identifier owns code and returns the current canonical
// code. If code is an old alias, the current longer code is returned.
//
// Resolve never uses the short code alone to decide identity: it looks up the
// identifier by its full MAC. A code owned by somebody else yields
// ErrIdentityMismatch. This is the operation that prevents two colliding people
// from being merged.
func (a *Allocator) Resolve(ctx context.Context, code, identifier string) (string, error) {
	ctx = nonNilContext(ctx)
	if identifier == "" {
		return "", ErrInvalidIdentifier
	}
	if code == "" {
		return "", ErrUnknownCode
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	digest := slices.Clone(a.mac([]byte(identifier)))
	if len(digest) == 0 {
		return "", fmt.Errorf("%w: MAC returned an empty digest", ErrInvalidOptions)
	}
	identity := hex.EncodeToString(digest)

	a.mu.Lock()
	defer a.mu.Unlock()

	rec, ok := a.state.records[identity]
	if !ok {
		if _, indexed := a.state.index[code]; indexed {
			return "", ErrIdentityMismatch
		}
		return "", ErrUnknownCode
	}
	if rec.Code == code || slices.Contains(rec.Aliases, code) {
		return rec.Code, nil
	}
	if _, indexed := a.state.index[code]; indexed {
		return "", ErrIdentityMismatch
	}
	return "", ErrUnknownCode
}

// Candidates returns the current canonical codes whose canonical code or alias
// equals code. More than one result means the short code alone is ambiguous and
// callers must use Resolve with the identifier before writing or joining events.
//
// Candidates is an index query. It deliberately does not merge entries and does
// not treat a shared prefix as evidence of a shared identity.
func (a *Allocator) Candidates(ctx context.Context, code string) ([]string, error) {
	ctx = nonNilContext(ctx)
	if code == "" {
		return nil, ErrUnknownCode
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	identities := a.state.index[code]
	if len(identities) == 0 {
		return nil, ErrUnknownCode
	}
	seen := make(map[string]struct{}, len(identities))
	codes := make([]string, 0, len(identities))
	for identity := range identities {
		rec := a.state.records[identity]
		if rec == nil {
			continue
		}
		if _, ok := seen[rec.Code]; ok {
			continue
		}
		seen[rec.Code] = struct{}{}
		codes = append(codes, rec.Code)
	}
	sort.Strings(codes)
	if len(codes) == 0 {
		return nil, ErrUnknownCode
	}
	return codes, nil
}

func (a *Allocator) save(ctx context.Context, st *state) error {
	raw, err := a.encodeSnapshot(st)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := a.store.Save(ctx, raw); err != nil {
		return fmt.Errorf("netsec: save store: %w", err)
	}
	return nil
}

func (a *Allocator) encodeSnapshot(st *state) ([]byte, error) {
	entries := make([]snapshotEntry, 0, len(st.records))
	for _, rec := range st.records {
		entries = append(entries, snapshotEntry{
			Identity: rec.Identity,
			Code:     rec.Code,
			Length:   rec.Length,
			Aliases:  slices.Clone(rec.Aliases),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Identity < entries[j].Identity
	})

	raw, err := json.MarshalIndent(snapshot{
		Version:           snapshotVersion,
		KeyID:             a.keyID,
		InitialCodeLength: a.initialLength,
		Entries:           entries,
	}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("netsec: encode snapshot: %w", err)
	}
	return append(raw, '\n'), nil
}

func (a *Allocator) decodeSnapshot(raw []byte) (*state, error) {
	var snap snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, fmt.Errorf("%w: decode JSON: %v", ErrCorruptStore, err)
	}
	if snap.Version != snapshotVersion {
		return nil, fmt.Errorf("%w: snapshot version %d", ErrIncompatibleStore, snap.Version)
	}
	if snap.KeyID != a.keyID {
		return nil, fmt.Errorf("%w: key check value differs", ErrIncompatibleStore)
	}
	if snap.InitialCodeLength != a.initialLength {
		return nil, fmt.Errorf("%w: initial code length changed from %d to %d",
			ErrIncompatibleStore, snap.InitialCodeLength, a.initialLength)
	}

	st := newState()
	for _, entry := range snap.Entries {
		digest, err := hex.DecodeString(entry.Identity)
		if err != nil || len(digest) == 0 || hex.EncodeToString(digest) != entry.Identity {
			return nil, fmt.Errorf("%w: invalid HMAC %q", ErrCorruptStore, entry.Identity)
		}
		encoded := encodeDigest(digest)
		if entry.Length < snap.InitialCodeLength || entry.Length > len(encoded) {
			return nil, fmt.Errorf("%w: invalid code length %d", ErrCorruptStore, entry.Length)
		}
		if entry.Code != encoded[:entry.Length] {
			return nil, fmt.Errorf("%w: short code %q does not match its HMAC", ErrCorruptStore, entry.Code)
		}
		wantAliases := prefixAliases(encoded, snap.InitialCodeLength, entry.Length)
		if !slices.Equal(entry.Aliases, wantAliases) {
			return nil, fmt.Errorf("%w: aliases for %q are inconsistent", ErrCorruptStore, entry.Code)
		}

		rec := &record{
			Identity: entry.Identity,
			Code:     entry.Code,
			Length:   entry.Length,
			Aliases:  slices.Clone(entry.Aliases),
		}
		if err := st.add(rec); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrCorruptStore, err)
		}
	}
	return st, nil
}

func newState() *state {
	return &state{
		records:   make(map[string]*record),
		canonical: make(map[string]string),
		index:     make(map[string]map[string]struct{}),
	}
}

func (s *state) clone() *state {
	next := newState()
	for _, rec := range s.records {
		_ = next.add(rec)
	}
	return next
}

func (s *state) allocate(identity, encoded string, initialLength int) (*record, error) {
	for length := initialLength; length <= len(encoded); length++ {
		code := encoded[:length]
		if _, taken := s.canonical[code]; taken {
			continue
		}
		rec := &record{
			Identity: identity,
			Code:     code,
			Length:   length,
			Aliases:  prefixAliases(encoded, initialLength, length),
		}
		if err := s.add(rec); err != nil {
			return nil, err
		}
		return rec, nil
	}
	return nil, ErrCodeSpaceExhausted
}

func (s *state) add(rec *record) error {
	if _, exists := s.records[rec.Identity]; exists {
		return fmt.Errorf("duplicate identity %q", rec.Identity)
	}
	if _, exists := s.canonical[rec.Code]; exists {
		return fmt.Errorf("duplicate canonical short code %q", rec.Code)
	}
	s.records[rec.Identity] = rec
	s.canonical[rec.Code] = rec.Identity
	addIndex := func(code, identity string) {
		set := s.index[code]
		if set == nil {
			set = make(map[string]struct{})
			s.index[code] = set
		}
		set[identity] = struct{}{}
	}
	addIndex(rec.Code, rec.Identity)
	for _, alias := range rec.Aliases {
		addIndex(alias, rec.Identity)
	}
	return nil
}

func prefixAliases(encoded string, initialLength, length int) []string {
	if length <= initialLength {
		return nil
	}
	aliases := make([]string, 0, length-initialLength)
	for n := initialLength; n < length; n++ {
		aliases = append(aliases, encoded[:n])
	}
	return aliases
}

func encodeDigest(digest []byte) string {
	return base32NoPadding.EncodeToString(digest)
}

func newHMAC(key []byte) MACFunc {
	key = slices.Clone(key)
	return func(identifier []byte) []byte {
		mac := hmac.New(sha256.New, key)
		_, _ = mac.Write([]byte(macDomain))
		_, _ = mac.Write(identifier)
		return mac.Sum(nil)
	}
}

func keyCheckValue(key []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(keyCheckDomain))
	return hex.EncodeToString(mac.Sum(nil))
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
