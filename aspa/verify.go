package aspa

import "sync"

// cacheKey uniquely identifies a verification: direction, address
// family, canonical path, and the snapshot serial. Because the serial
// is part of the key, a snapshot republish can never resurrect or
// contaminate results computed from revoked or expired objects.
type cacheKey struct {
	dir    Direction
	afi    AFI
	path   string
	serial uint64
}

// Verifier performs ASPA path verification for the BGP decision
// process. It is safe for concurrent use.
type Verifier struct {
	store *Store

	mu    sync.Mutex
	cache map[cacheKey]Result
}

// NewVerifier returns a Verifier reading snapshots from store.
func NewVerifier(store *Store) *Verifier {
	return &Verifier{store: store, cache: make(map[cacheKey]Result)}
}

// Verify checks path (AS_PATH segments in received order) for a route
// arriving over a session with the given neighbor relationship and
// address family. The verification direction is derived from the
// relationship: provider sessions verify downstream, all others
// upstream.
func (v *Verifier) Verify(path []Segment, rel NeighborRelation, afi AFI) Result {
	return v.VerifyDirection(path, DirectionForRelation(rel), afi)
}

// VerifyDirection is like Verify but takes the propagation direction
// explicitly.
func (v *Verifier) VerifyDirection(path []Segment, dir Direction, afi AFI) Result {
	// Pin the snapshot for the whole verification: even if a new
	// snapshot is published mid-check, this path is evaluated
	// entirely against one serial.
	snap := v.store.Current()
	if snap == nil {
		return Result{
			State:     StateUnknown,
			Direction: dir,
			AFI:       afi,
			Reason:    "no RPKI snapshot published",
		}
	}

	seq, canonical, hasSet := normalize(path)
	key := cacheKey{dir: dir, afi: afi, path: canonical, serial: snap.Serial}

	v.mu.Lock()
	res, ok := v.cache[key]
	v.mu.Unlock()
	if ok {
		return res
	}

	if hasSet {
		res = Result{
			State:     StateUnknown,
			Direction: dir,
			AFI:       afi,
			Serial:    snap.Serial,
			Reason:    "AS_SET present: adjacency cannot be determined",
		}
	} else {
		res = verifySequence(seq, snap, dir, afi)
	}

	v.mu.Lock()
	v.cache[key] = res
	v.mu.Unlock()
	return res
}

// verifySequence checks the normalized, neighbor-first sequence
// against the pinned snapshot. The same ramp algorithm applies to
// both propagation directions; the direction selects which neighbor
// the sequence was received from and is carried into the result and
// cache key.
//
// The algorithm walks an up-ramp of customer-to-provider authorized
// pairs from the neighbor side and a down-ramp of provider-to-customer
// authorized pairs from the far end. If the ramps meet or overlap the
// path is Valid. A gap is a valley or leak: the first failing pair is
// Invalid when the customer's ASPA record exists but does not
// authorize the provider, and Unknown when no record exists.
func verifySequence(seq []ASN, snap *Snapshot, dir Direction, afi AFI) Result {
	res := Result{State: StateValid, Direction: dir, AFI: afi, Serial: snap.Serial}
	if len(seq) < 2 {
		return res
	}

	pairs := len(seq) - 1
	upOK := make([]bool, pairs)
	downOK := make([]bool, pairs)
	for i := 0; i < pairs; i++ {
		customer, provider := seq[i], seq[i+1]
		if customer == provider {
			// Prepend / duplicate AS: a self-pair is trivially
			// satisfied in both directions.
			upOK[i], downOK[i] = true, true
			continue
		}
		upOK[i] = snap.Authorized(customer, provider)
		downOK[i] = snap.Authorized(provider, customer)
	}

	// Up-ramp: longest prefix of pairs authorized customer->provider.
	left := 0
	for left < pairs && upOK[left] {
		left++
	}
	// Down-ramp: longest suffix of pairs authorized provider->customer.
	right := pairs - 1
	for right >= 0 && downOK[right] {
		right--
	}

	if left > right {
		return res
	}

	// The first pair of the gap (index left) always fails the
	// up-ramp check; it is the first conflict along the propagation
	// direction.
	customer, provider := seq[left], seq[left+1]
	res.Conflict = &Conflict{
		PairIndex: left,
		Customer:  customer,
		Provider:  provider,
		ASPA:      snap.Record(customer),
	}
	if !snap.HasRecord(customer) {
		res.State = StateUnknown
		res.Reason = "no ASPA record for customer in failing pair"
		return res
	}
	res.State = StateInvalid
	res.Reason = "provider not authorized by customer ASPA (valley or route leak)"
	return res
}
