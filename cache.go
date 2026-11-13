package netsec

import (
	"crypto/ed25519"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// CacheConfig initialises an RPKI cache. Session is optional; a random,
// non-zero session is generated when it is zero. VRPs must already represent
// a complete view at Serial.
type CacheConfig struct {
	CacheID    string
	PrivateKey ed25519.PrivateKey
	Session    SessionID
	Serial     uint32
	VRPs       []VRP
	MaxHistory int
}

// Cache is the cache-side state machine. It signs every response fragment,
// keeps a bounded serial history for resumption, and never publishes a
// partially-applied set.
type Cache struct {
	cacheID string
	private ed25519.PrivateKey
	public  ed25519.PublicKey

	mu         sync.RWMutex
	session    SessionID
	serial     uint32
	set        map[string]vrpRecord
	sorted     []vrpRecord
	root       Digest
	steps      map[uint32]deltaStep
	history    []uint32
	maxHistory int
	updatedAt  time.Time
}

type deltaStep struct {
	from    uint32
	to      uint32
	changes []Change
}

const defaultMaxHistory = 4096

// NewCache creates a cache with a random session and an empty set at serial 0.
func NewCache(cacheID string, privateKey ed25519.PrivateKey) (*Cache, error) {
	return NewCacheWithConfig(CacheConfig{CacheID: cacheID, PrivateKey: privateKey})
}

// NewCacheWithSeed creates a cache with a deterministic Ed25519 key.
func NewCacheWithSeed(cacheID string, seed []byte) (*Cache, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("netsec: seed is %d bytes, want %d", len(seed), ed25519.SeedSize)
	}
	return NewCache(cacheID, ed25519.NewKeyFromSeed(seed))
}

// NewCacheWithConfig creates a cache at an explicitly supplied state. It is
// useful for restored caches and for testing serial wrap-around.
func NewCacheWithConfig(config CacheConfig) (*Cache, error) {
	if config.CacheID == "" {
		return nil, fmt.Errorf("netsec: empty cache ID")
	}
	if len(config.PrivateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("netsec: private key is %d bytes, want %d", len(config.PrivateKey), ed25519.PrivateKeySize)
	}
	session := config.Session
	if session.IsZero() {
		var err error
		session, err = NewSessionID()
		if err != nil {
			return nil, err
		}
	}

	set, sorted, err := canonicalVRPSet(config.VRPs)
	if err != nil {
		return nil, err
	}
	root, err := rootFromSorted(sorted)
	if err != nil {
		return nil, err
	}
	historySize := config.MaxHistory
	if historySize <= 0 {
		historySize = defaultMaxHistory
	}
	priv := append(ed25519.PrivateKey(nil), config.PrivateKey...)
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("netsec: private key has no Ed25519 public key")
	}
	return &Cache{
		cacheID:    config.CacheID,
		private:    priv,
		public:     append(ed25519.PublicKey(nil), pub...),
		session:    session,
		serial:     config.Serial,
		set:        set,
		sorted:     sorted,
		root:       root,
		steps:      make(map[uint32]deltaStep),
		maxHistory: historySize,
		updatedAt:  time.Now(),
	}, nil
}

func rootFromSorted(sorted []vrpRecord) (Digest, error) {
	vrps := make([]VRP, len(sorted))
	for i := range sorted {
		vrps[i] = sorted[i].vrp
	}
	return ComputeRoot(vrps)
}

func (c *Cache) CacheID() CacheID     { return c.cacheID }
func (c *Cache) Session() SessionID   { c.mu.RLock(); defer c.mu.RUnlock(); return c.session }
func (c *Cache) Serial() uint32       { c.mu.RLock(); defer c.mu.RUnlock(); return c.serial }
func (c *Cache) RootDigest() Digest   { c.mu.RLock(); defer c.mu.RUnlock(); return c.root }
func (c *Cache) UpdatedAt() time.Time { c.mu.RLock(); defer c.mu.RUnlock(); return c.updatedAt }

func (c *Cache) PublicKey() ed25519.PublicKey {
	return append(ed25519.PublicKey(nil), c.public...)
}

func (c *Cache) VRPs() []VRP {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]VRP, len(c.sorted))
	for i := range c.sorted {
		out[i] = c.sorted[i].vrp
	}
	return out
}

// State returns a coherent copy of the cache's current view.
func (c *Cache) State() (SessionID, uint32, []VRP, Digest) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	vrps := make([]VRP, len(c.sorted))
	for i := range c.sorted {
		vrps[i] = c.sorted[i].vrp
	}
	return c.session, c.serial, vrps, c.root
}

// ApplyChanges atomically applies all changes as one serial step. The serial
// advances by exactly one even when several VRPs change in the same step.
func (c *Cache) ApplyChanges(changes []Change) (uint32, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.applyChangesLocked(changes)
}

func (c *Cache) applyChangesLocked(changes []Change) (uint32, error) {
	if len(changes) == 0 {
		return c.serial, nil
	}

	nextSet := cloneVRPMap(c.set)
	canonicalChanges := make([]Change, 0, len(changes))
	for _, ch := range changes {
		rec, err := canonicalVRP(ch.VRP)
		if err != nil {
			return c.serial, err
		}
		switch ch.Operation {
		case ChangeAdd:
			if _, exists := nextSet[rec.key]; exists {
				return c.serial, fmt.Errorf("%w: %s", ErrAddConflict, rec.key)
			}
			nextSet[rec.key] = rec
		case ChangeRemove:
			if _, exists := nextSet[rec.key]; !exists {
				return c.serial, fmt.Errorf("%w: %s", ErrRemoveMissing, rec.key)
			}
			delete(nextSet, rec.key)
		default:
			return c.serial, fmt.Errorf("%w: %d", ErrInvalidOperation, ch.Operation)
		}
		canonicalChanges = append(canonicalChanges, Change{Operation: ch.Operation, VRP: rec.vrp})
	}

	nextSerial := c.serial + 1
	nextSorted := sortedFromMap(nextSet)
	nextRoot, err := rootFromSorted(nextSorted)
	if err != nil {
		return c.serial, err
	}

	c.set = nextSet
	c.sorted = nextSorted
	c.serial = nextSerial
	c.root = nextRoot
	c.updatedAt = time.Now()
	c.rememberStep(deltaStep{
		from:    c.serial - 1,
		to:      c.serial,
		changes: canonicalChanges,
	})
	return c.serial, nil
}

// ReplaceVRPs atomically publishes a complete new VRP set. The cache computes
// the Add/Remove delta itself and advances the serial once if anything
// changed.
func (c *Cache) ReplaceVRPs(vrps []VRP) (uint32, error) {
	targetSet, _, err := canonicalVRPSet(vrps)
	if err != nil {
		return 0, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	var changes []Change
	for key, oldRec := range c.set {
		if _, keep := targetSet[key]; !keep {
			changes = append(changes, Change{Operation: ChangeRemove, VRP: oldRec.vrp})
		}
	}
	for key, newRec := range targetSet {
		if _, exists := c.set[key]; !exists {
			changes = append(changes, Change{Operation: ChangeAdd, VRP: newRec.vrp})
		}
	}
	if len(changes) == 0 {
		return c.serial, nil
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Operation != changes[j].Operation {
			return changes[i].Operation < changes[j].Operation
		}
		return changes[i].VRP.Key() < changes[j].VRP.Key()
	})
	return c.applyChangesLocked(changes)
}

// RotateSession starts a new random session and clears serial history. A
// router must use a Reset Query to consume the first view in the new session.
func (c *Cache) RotateSession() (SessionID, error) {
	session, err := NewSessionID()
	if err != nil {
		return SessionID{}, err
	}
	return c.rotateToSession(session)
}

func (c *Cache) rotateToSession(session SessionID) (SessionID, error) {
	if session.IsZero() {
		return SessionID{}, fmt.Errorf("%w: zero session ID", ErrMalformedMessage)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.session = session
	c.serial = 0
	c.steps = make(map[uint32]deltaStep)
	c.history = nil
	c.updatedAt = time.Now()
	return session, nil
}

func (c *Cache) rememberStep(step deltaStep) {
	if old, exists := c.steps[step.to]; exists {
		for i, serial := range c.history {
			if serial == old.to {
				c.history = append(c.history[:i], c.history[i+1:]...)
				break
			}
		}
	}
	c.steps[step.to] = step
	c.history = append(c.history, step.to)
	for len(c.history) > c.maxHistory {
		oldest := c.history[0]
		c.history = c.history[1:]
		delete(c.steps, oldest)
	}
}

func cloneVRPMap(src map[string]vrpRecord) map[string]vrpRecord {
	dst := make(map[string]vrpRecord, len(src))
	for key, rec := range src {
		dst[key] = rec
	}
	return dst
}

func sortedFromMap(set map[string]vrpRecord) []vrpRecord {
	sorted := make([]vrpRecord, 0, len(set))
	for _, rec := range set {
		sorted = append(sorted, rec)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].key < sorted[j].key })
	return sorted
}

// HandleQuery builds a complete signed response for one query.
func (c *Cache) HandleQuery(query Query) ([]SignedMessage, error) {
	if err := query.Validate(); err != nil {
		return nil, err
	}
	if query.CacheID != c.cacheID {
		return nil, fmt.Errorf("%w: query is for %q, cache is %q", ErrUnknownCache, query.CacheID, c.cacheID)
	}
	switch query.Kind {
	case QueryReset:
		return c.ResetResponse(query)
	case QuerySerial:
		return c.SerialResponse(query)
	default:
		return nil, fmt.Errorf("%w: query kind %d", ErrMalformedMessage, query.Kind)
	}
}

// ResetResponse returns Begin(session,serial), one signed message per VRP,
// and End(session,serial,rootDigest).
func (c *Cache) ResetResponse(query Query) ([]SignedMessage, error) {
	if query.Kind != QueryReset {
		return nil, fmt.Errorf("%w: reset response requires a reset query", ErrMalformedMessage)
	}
	if query.CacheID != c.cacheID {
		return nil, fmt.Errorf("%w: query is for %q, cache is %q", ErrUnknownCache, query.CacheID, c.cacheID)
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.sorted) > math.MaxUint32-2 {
		return nil, fmt.Errorf("%w: VRP set is too large", ErrMalformedMessage)
	}
	total := uint32(len(c.sorted)) + 2
	out := make([]SignedMessage, 0, total)

	begin := SignedMessage{
		CacheID:              c.cacheID,
		ConnectionGeneration: query.ConnectionGeneration,
		RequestID:            query.RequestID,
		Kind:                 MessageResetBegin,
		Index:                0,
		Total:                total,
		Session:              c.session,
		Serial:               c.serial,
		ToSerial:             c.serial,
	}
	signed, err := SignMessage(c.private, begin)
	if err != nil {
		return nil, err
	}
	out = append(out, signed)

	for i, rec := range c.sorted {
		msg := SignedMessage{
			CacheID:              c.cacheID,
			ConnectionGeneration: query.ConnectionGeneration,
			RequestID:            query.RequestID,
			Kind:                 MessageResetVRP,
			Index:                uint32(i + 1),
			Total:                total,
			Session:              c.session,
			Serial:               c.serial,
			ToSerial:             c.serial,
			VRP:                  rec.vrp,
		}
		signed, err = SignMessage(c.private, msg)
		if err != nil {
			return nil, err
		}
		out = append(out, signed)
	}

	last := SignedMessage{
		CacheID:              c.cacheID,
		ConnectionGeneration: query.ConnectionGeneration,
		RequestID:            query.RequestID,
		Kind:                 MessageResetEnd,
		Index:                total - 1,
		Total:                total,
		Session:              c.session,
		Serial:               c.serial,
		ToSerial:             c.serial,
		RootDigest:           c.root,
	}
	signed, err = SignMessage(c.private, last)
	if err != nil {
		return nil, err
	}
	out = append(out, signed)
	return out, nil
}

// SerialResponse returns signed Add/Remove events covering every serial from
// query.FromSerial+1 through the current serial, followed by End.
func (c *Cache) SerialResponse(query Query) ([]SignedMessage, error) {
	if query.Kind != QuerySerial {
		return nil, fmt.Errorf("%w: serial response requires a serial query", ErrMalformedMessage)
	}
	if query.CacheID != c.cacheID {
		return nil, fmt.Errorf("%w: query is for %q, cache is %q", ErrUnknownCache, query.CacheID, c.cacheID)
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	if query.FromSerial != c.serial && !IsForwardSerial(query.FromSerial, c.serial) {
		return nil, fmt.Errorf("%w: query from serial %d, cache serial %d", ErrSerialRegression, query.FromSerial, c.serial)
	}

	steps, err := c.chainFrom(query.FromSerial)
	if err != nil {
		return nil, err
	}
	changeCount := 0
	for _, step := range steps {
		if len(step.changes) > math.MaxUint32-changeCount-1 {
			return nil, fmt.Errorf("%w: delta is too large", ErrMalformedMessage)
		}
		changeCount += len(step.changes)
	}
	if changeCount > math.MaxUint32-1 {
		return nil, fmt.Errorf("%w: delta is too large", ErrMalformedMessage)
	}
	total := uint32(changeCount) + 1
	out := make([]SignedMessage, 0, total)
	index := uint32(0)

	for _, step := range steps {
		for _, change := range step.changes {
			kind := MessageAdd
			if change.Operation == ChangeRemove {
				kind = MessageRemove
			}
			msg := SignedMessage{
				CacheID:              c.cacheID,
				ConnectionGeneration: query.ConnectionGeneration,
				RequestID:            query.RequestID,
				Kind:                 kind,
				Index:                index,
				Total:                total,
				Session:              c.session,
				Serial:               step.to,
				FromSerial:           query.FromSerial,
				ToSerial:             c.serial,
				VRP:                  change.VRP,
			}
			signed, err := SignMessage(c.private, msg)
			if err != nil {
				return nil, err
			}
			out = append(out, signed)
			index++
		}
	}

	end := SignedMessage{
		CacheID:              c.cacheID,
		ConnectionGeneration: query.ConnectionGeneration,
		RequestID:            query.RequestID,
		Kind:                 MessageSerialEnd,
		Index:                total - 1,
		Total:                total,
		Session:              c.session,
		Serial:               c.serial,
		FromSerial:           query.FromSerial,
		ToSerial:             c.serial,
		RootDigest:           c.root,
	}
	signed, err := SignMessage(c.private, end)
	if err != nil {
		return nil, err
	}
	out = append(out, signed)
	return out, nil
}

func (c *Cache) chainFrom(from uint32) ([]deltaStep, error) {
	if from == c.serial {
		return nil, nil
	}
	var reversed []deltaStep
	current := c.serial
	for current != from {
		step, exists := c.steps[current]
		if !exists {
			return nil, fmt.Errorf("%w: no delta for serial %d", ErrHistoryUnavailable, current)
		}
		if step.to != current {
			return nil, fmt.Errorf("%w: corrupt serial history", ErrInvalidState)
		}
		reversed = append(reversed, step)
		current = step.from
		if len(reversed) > len(c.steps)+1 {
			return nil, fmt.Errorf("%w: serial history loop", ErrInvalidState)
		}
	}
	steps := make([]deltaStep, len(reversed))
	for i := range reversed {
		steps[len(reversed)-1-i] = reversed[i]
	}
	for i := 1; i < len(steps); i++ {
		if steps[i].from != steps[i-1].to {
			return nil, fmt.Errorf("%w: serial history is discontinuous", ErrInvalidState)
		}
	}
	if len(steps) > 0 && steps[0].from != from {
		return nil, fmt.Errorf("%w: serial history starts at %d", ErrInvalidState, steps[0].from)
	}
	return steps, nil
}
