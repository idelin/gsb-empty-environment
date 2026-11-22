package changeaccept

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
)

// Hop is the exact, already-observed forwarding decision to cache. Every field
// participates in the key; History prevents a verified hop from another label
// history being reused for this connection.
type Hop struct {
	ConnectionID string
	Generation   Generation
	Pair         TenantPair
	From         EndpointID
	To           EndpointID
	FromLabel    Label
	ToLabel      Label
	History      [32]byte
	// SnapshotDigest is optional but recommended. It disambiguates distinct
	// policy snapshots that happen to carry the same numeric generation.
	SnapshotDigest string
}

// HopFor derives a concrete hop from a connection's immutable label history.
func HopFor(connection Connection, hopIndex int, generation Generation) (Hop, error) {
	if hopIndex < 0 || hopIndex+1 >= len(connection.Path) || len(connection.Path) != len(connection.Labels) {
		return Hop{}, fmt.Errorf("hop index %d is outside connection %q", hopIndex, connection.ID)
	}
	history := HistoryDigest(connection.Path[:hopIndex+2], connection.Labels[:hopIndex+2])
	return Hop{
		ConnectionID: connection.ID,
		Generation:   generation,
		Pair:         connection.Pair,
		From:         connection.Path[hopIndex],
		To:           connection.Path[hopIndex+1],
		FromLabel:    connection.Labels[hopIndex],
		ToLabel:      connection.Labels[hopIndex+1],
		History:      history,
	}, nil
}

// HopForSnapshot also binds the exact snapshot fingerprint, eliminating cache
// ambiguity between alternative candidate branches with equal generation
// numbers.
func HopForSnapshot(connection Connection, hopIndex int, snapshot Snapshot) (Hop, error) {
	hop, err := HopFor(connection, hopIndex, snapshot.Generation)
	if err != nil {
		return Hop{}, err
	}
	hop.SnapshotDigest = SnapshotFingerprint(snapshot)
	return hop, nil
}

// HistoryDigest hashes a path prefix and its complete label history. Lengths
// must match for a non-zero digest.
func HistoryDigest(path []EndpointID, labels []Label) [32]byte {
	if len(path) != len(labels) || len(path) == 0 {
		return [32]byte{}
	}
	encoded, _ := json.Marshal(struct {
		Path   []EndpointID `json:"path"`
		Labels []Label      `json:"labels"`
	}{Path: path, Labels: labels})
	return sha256.Sum256(encoded)
}

func (h Hop) validate() error {
	if h.ConnectionID == "" {
		return fmt.Errorf("hop has no connection ID")
	}
	if h.Pair.Source == "" || h.Pair.Destination == "" {
		return fmt.Errorf("hop has incomplete tenant pair")
	}
	if h.From == "" || h.To == "" {
		return fmt.Errorf("hop has incomplete endpoints")
	}
	if h.History == ([32]byte{}) {
		return fmt.Errorf("hop has no label-history digest")
	}
	return nil
}

// CacheStatus distinguishes a real decision from a cache hit.
type CacheStatus uint8

const (
	CacheMiss CacheStatus = iota
	CacheHit
	CacheRejected
)

func (s CacheStatus) String() string {
	switch s {
	case CacheMiss:
		return "miss"
	case CacheHit:
		return "hit"
	case CacheRejected:
		return "rejected"
	default:
		return "unknown"
	}
}

// CacheOutcome reports the decision and whether it came from the tracker.
type CacheOutcome struct {
	Allowed bool
	Hit     bool
	Status  CacheStatus
}

type hopCacheKey struct {
	ConnectionID   string
	Generation     Generation
	Pair           TenantPair
	From           EndpointID
	To             EndpointID
	FromLabel      Label
	ToLabel        Label
	History        [32]byte
	SnapshotDigest string
}

func cacheKey(hop Hop) hopCacheKey {
	return hopCacheKey{
		ConnectionID:   hop.ConnectionID,
		Generation:     hop.Generation,
		Pair:           hop.Pair,
		From:           hop.From,
		To:             hop.To,
		FromLabel:      hop.FromLabel,
		ToLabel:        hop.ToLabel,
		History:        hop.History,
		SnapshotDigest: hop.SnapshotDigest,
	}
}

// Tracker caches only successful decisions for concrete hops. Failed or
// malformed hops are never made reusable by a wildcard key.
type Tracker struct {
	mu      sync.RWMutex
	entries map[hopCacheKey]struct{}
	hits    int
	misses  int
}

// NewTracker returns an empty connection tracker.
func NewTracker() *Tracker {
	return &Tracker{entries: make(map[hopCacheKey]struct{})}
}

// Verify returns a cached approval for the exact hop or invokes checker on a
// miss. A malformed hop or nil checker fails closed without touching the cache.
func (t *Tracker) Verify(hop Hop, checker func(Hop) bool) CacheOutcome {
	if t == nil || hop.validate() != nil || checker == nil {
		return CacheOutcome{Allowed: false, Status: CacheRejected}
	}
	key := cacheKey(hop)
	t.mu.RLock()
	_, hit := t.entries[key]
	t.mu.RUnlock()
	if hit {
		t.mu.Lock()
		t.hits++
		t.mu.Unlock()
		return CacheOutcome{Allowed: true, Hit: true, Status: CacheHit}
	}
	allowed := checker(hop)
	if allowed {
		t.mu.Lock()
		t.entries[key] = struct{}{}
		t.misses++
		t.mu.Unlock()
	} else {
		t.mu.Lock()
		t.misses++
		t.mu.Unlock()
	}
	return CacheOutcome{Allowed: allowed, Hit: false, Status: CacheMiss}
}

// CheckHop re-evaluates both policies and the label change for exactly one hop.
// Tenant and history binding are deliberately the tracker's responsibility.
func (s Snapshot) CheckHop(hop Hop) bool {
	endpoints := s.Topology.endpointMap()
	from, ok := endpoints[hop.From]
	if !ok {
		return false
	}
	to, ok := endpoints[hop.To]
	if !ok {
		return false
	}
	if !from.Egress.Decide(hop.FromLabel).allowed() {
		return false
	}
	labelAllowed := false
	for _, candidate := range from.Changes.Next(hop.FromLabel) {
		if candidate == hop.ToLabel {
			labelAllowed = true
			break
		}
	}
	if !labelAllowed {
		return false
	}
	return to.Ingress.Decide(hop.ToLabel).allowed()
}

// VerifyConnection verifies each concrete hop through the tracker.
func (t *Tracker) VerifyConnection(connection Connection, snapshot Snapshot) ([]CacheOutcome, error) {
	if len(connection.Path) < 2 || len(connection.Path) != len(connection.Labels) {
		return nil, fmt.Errorf("connection %q has an invalid path/label history", connection.ID)
	}
	outcomes := make([]CacheOutcome, 0, len(connection.Path)-1)
	snapshotDigest := SnapshotFingerprint(snapshot)
	for index := 0; index < len(connection.Path)-1; index++ {
		hop, err := HopFor(connection, index, snapshot.Generation)
		if err != nil {
			return nil, err
		}
		hop.SnapshotDigest = snapshotDigest
		outcome := t.Verify(hop, snapshot.CheckHop)
		outcomes = append(outcomes, outcome)
		if !outcome.Allowed {
			return outcomes, fmt.Errorf("connection %q hop %d rejected", connection.ID, index)
		}
	}
	return outcomes, nil
}

// Stats returns cumulative cache hits and misses.
func (t *Tracker) Stats() (hits, misses int) {
	if t == nil {
		return 0, 0
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.hits, t.misses
}
