package netsec

import (
	"crypto/hmac"
	"crypto/rand"
	"fmt"
	"io"
	"sync"
	"time"
)

const (
	defaultChallengeTTL      = 30 * time.Second
	defaultFailureLimit      = 5
	defaultFailureWindow     = time.Minute
	defaultMaxFragmentBytes  = 1 << 20
	defaultMaxPendingPerConn = 64
)

// Config configures a Gateway.  Keys are copied at construction time and are
// never replaced afterwards; a response may only select one of these key ids.
type Config struct {
	// Keys is the fixed master-key table.  Key material is never accepted from
	// an authentication response.
	Keys map[KeyID][]byte

	// KeyByMaster optionally selects a key id for each master address.
	KeyByMaster map[uint16]KeyID

	// DefaultKeyID selects the key when KeyByMaster has no entry.  If it is
	// empty and exactly one key exists, that key is used.
	DefaultKeyID KeyID

	// ChallengeTTL is the lifetime of a pending challenge.  The deadline is
	// derived from Clock and retains the monotonic component of time.Now when
	// the default clock is used.
	ChallengeTTL time.Duration

	// MaxFailedResponses is the number of failed responses allowed per master
	// in FailureWindow.  Zero selects the default (5); a negative value disables
	// failure rate limiting.
	MaxFailedResponses int

	// FailureWindow is the fixed window used for failed-response accounting.
	FailureWindow time.Duration

	// MaxPendingPerConnection limits simultaneously pending challenges on one
	// connection.  Zero selects the default (64); a negative value disables the
	// limit.
	MaxPendingPerConnection int

	// MaxFragmentBytes limits the total reassembled operation value.
	MaxFragmentBytes int

	// PolicyVersion is included in the challenge and every receipt.  Calling
	// SetPolicyVersion with a different value cancels outstanding challenges and
	// incomplete fragment assemblies.
	PolicyVersion string

	// Executor is called exactly once for each authorised request.
	Executor Executor

	// Clock is injectable for tests.  time.Now is used when nil.
	Clock func() time.Time

	// Rand supplies challenge ids and nonces.  crypto/rand.Reader is used when
	// nil.
	Rand io.Reader
}

// Gateway owns the fixed key table, pending challenges, fragment assemblies,
// execution receipts, and per-master failed-response accounting.
type Gateway struct {
	mu sync.Mutex

	keys        map[KeyID][]byte
	keyByMaster map[uint16]KeyID
	defaultKey  KeyID

	ttl              time.Duration
	failureLimit     int
	failureWindow    time.Duration
	maxPending       int
	maxFragmentBytes int

	now      func() time.Time
	lastNow  time.Time
	random   io.Reader
	executor Executor

	policy string

	connections map[ConnectionID]*connectionState
	pending     map[ChallengeID]*challengeEntry
	challenges  map[ChallengeID]*challengeEntry
	assemblies  map[fragmentKey]*assembly
	rates       map[uint16]*rateBucket
}

type connectionState struct {
	reset        bool
	haveSequence bool
	highestSeq   uint32
	pending      map[uint32]ChallengeID
	challengeIDs map[ChallengeID]struct{}
}

type challengeEntry struct {
	challenge    Challenge
	state        ChallengeState
	cancelReason CancelReason
	done         chan struct{}
	receipt      *Receipt
}

type rateBucket struct {
	windowStart time.Time
	failures    int
}

// NewGateway validates and freezes the key table and defaults.
func NewGateway(cfg Config) (*Gateway, error) {
	if len(cfg.Keys) == 0 {
		return nil, fmt.Errorf("%w: %w", ErrInvalidConfig, ErrNoKeys)
	}
	keys := make(map[KeyID][]byte, len(cfg.Keys))
	for id, secret := range cfg.Keys {
		if id == "" || len(secret) == 0 {
			return nil, fmt.Errorf("%w: empty key id or secret", ErrInvalidConfig)
		}
		keys[id] = cloneBytes(secret)
	}
	if cfg.DefaultKeyID != "" {
		if _, ok := keys[cfg.DefaultKeyID]; !ok {
			return nil, fmt.Errorf("%w: default key %q is not in key table", ErrInvalidConfig, cfg.DefaultKeyID)
		}
	}
	byMaster := make(map[uint16]KeyID, len(cfg.KeyByMaster))
	for master, id := range cfg.KeyByMaster {
		if _, ok := keys[id]; !ok {
			return nil, fmt.Errorf("%w: key %q for master %d is not in key table", ErrInvalidConfig, id, master)
		}
		byMaster[master] = id
	}
	if len(byMaster) == 0 && cfg.DefaultKeyID == "" && len(keys) != 1 {
		return nil, fmt.Errorf("%w: set DefaultKeyID or KeyByMaster", ErrInvalidConfig)
	}

	ttl := cfg.ChallengeTTL
	if ttl <= 0 {
		ttl = defaultChallengeTTL
	}
	limit := cfg.MaxFailedResponses
	if limit == 0 {
		limit = defaultFailureLimit
	}
	window := cfg.FailureWindow
	if window <= 0 {
		window = defaultFailureWindow
	}
	maxPending := cfg.MaxPendingPerConnection
	if maxPending == 0 {
		maxPending = defaultMaxPendingPerConn
	}
	maxFragment := cfg.MaxFragmentBytes
	if maxFragment <= 0 {
		maxFragment = defaultMaxFragmentBytes
	}
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	random := cfg.Rand
	if random == nil {
		random = rand.Reader
	}
	exec := cfg.Executor
	if exec == nil {
		exec = func(Execution) ([]byte, error) { return nil, nil }
	}

	return &Gateway{
		keys:             keys,
		keyByMaster:      byMaster,
		defaultKey:       cfg.DefaultKeyID,
		ttl:              ttl,
		failureLimit:     limit,
		failureWindow:    window,
		maxPending:       maxPending,
		maxFragmentBytes: maxFragment,
		now:              clock,
		random:           random,
		executor:         exec,
		policy:           cfg.PolicyVersion,
		connections:      make(map[ConnectionID]*connectionState),
		pending:          make(map[ChallengeID]*challengeEntry),
		challenges:       make(map[ChallengeID]*challengeEntry),
		assemblies:       make(map[fragmentKey]*assembly),
		rates:            make(map[uint16]*rateBucket),
	}, nil
}

// Submit freezes a high-risk request and issues a pending challenge.  The
// operation is not executed until a valid response arrives.
func (g *Gateway) Submit(id RequestIdentity) (*Challenge, error) {
	frozen, err := freezeIdentity(id)
	if err != nil {
		return nil, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.nowLocked()
	g.expireLocked(now)
	challenge, err := g.submitLocked(frozen, now)
	if err != nil {
		return nil, err
	}
	return cloneChallenge(challenge), nil
}

// AddFragment feeds one application fragment to the reassembler.  complete is
// false until the final fragment has arrived; only then is a challenge returned
// and only then can the request be authorised.  Fragments with mismatching
// identity fields, gaps, duplicates, or writes after the final fragment are
// rejected and the partial assembly is discarded.
func (g *Gateway) AddFragment(f Fragment) (challenge *Challenge, complete bool, err error) {
	data := cloneBytes(f.Data)
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.nowLocked()
	g.expireLocked(now)

	key := f.assemblyKey()
	header := f.header()
	conn := g.connectionLocked(key.connection)
	if conn.reset {
		return nil, false, ErrConnectionReset
	}

	a, exists := g.assemblies[key]
	if !exists {
		if f.FragmentIndex != 0 {
			return nil, false, fmt.Errorf("%w: first fragment index must be 0", ErrFragmentGap)
		}
		if g.maxPending >= 0 {
			count := 0
			for other := range g.assemblies {
				if other.connection == key.connection {
					count++
				}
			}
			if count >= g.maxPending {
				return nil, false, ErrTooManyPending
			}
		}
		a = &assembly{
			header:   header,
			deadline: now.Add(g.ttl),
		}
		g.assemblies[key] = a
	} else {
		if a.header != header {
			delete(g.assemblies, key)
			return nil, false, ErrFragmentMismatch
		}
		if f.FragmentIndex < a.nextIndex {
			delete(g.assemblies, key)
			return nil, false, fmt.Errorf("%w: got %d, expected %d", ErrDuplicateFragment, f.FragmentIndex, a.nextIndex)
		}
		if f.FragmentIndex > a.nextIndex {
			delete(g.assemblies, key)
			return nil, false, fmt.Errorf("%w: got %d, expected %d", ErrFragmentGap, f.FragmentIndex, a.nextIndex)
		}
	}

	if a.buffer.Len()+len(data) > g.maxFragmentBytes {
		delete(g.assemblies, key)
		return nil, false, ErrFragmentTooLarge
	}
	_, _ = a.buffer.Write(data)
	a.nextIndex++

	if !f.Final {
		return nil, false, nil
	}
	delete(g.assemblies, key)

	frozen, err := freezeIdentity(f.identity(a.buffer.Bytes()))
	if err != nil {
		return nil, true, err
	}
	ch, err := g.submitLocked(frozen, now)
	if err != nil {
		return nil, true, err
	}
	return cloneChallenge(ch), true, nil
}

// Respond verifies a challenge response.  On success the matching pending
// challenge is atomically moved to Authorized, the original request is
// executed once, and a durable receipt is stored.  A retransmission of the same
// valid response returns that receipt without executing again.
func (g *Gateway) Respond(resp Response) (Receipt, error) {
	g.mu.Lock()
	now := g.nowLocked()
	g.expireLocked(now)

	entry, ok := g.challenges[resp.ChallengeID]
	if !ok {
		g.mu.Unlock()
		return Receipt{}, ErrUnknownChallenge
	}

	switch entry.state {
	case StateCompleted:
		if err := g.verifyLocked(entry, resp); err != nil {
			g.recordFailureLocked(entry.challenge.Identity.MasterAddress, now)
			g.mu.Unlock()
			return Receipt{}, err
		}
		receipt := entry.receipt.Clone()
		g.mu.Unlock()
		return receipt, nil

	case StateAuthorized:
		if err := g.verifyLocked(entry, resp); err != nil {
			g.recordFailureLocked(entry.challenge.Identity.MasterAddress, now)
			g.mu.Unlock()
			return Receipt{}, err
		}
		done := entry.done
		g.mu.Unlock()
		<-done
		g.mu.Lock()
		receipt := entry.receipt.Clone()
		g.mu.Unlock()
		return receipt, nil

	case StateCanceled:
		g.recordFailureLocked(entry.challenge.Identity.MasterAddress, now)
		reason := entry.cancelReason
		g.mu.Unlock()
		if reason == CancelExpired {
			return Receipt{}, fmt.Errorf("%w: %s", ErrChallengeExpired, reason)
		}
		return Receipt{}, fmt.Errorf("%w: %s", ErrChallengeCanceled, reason)

	case StatePending:
		// Handled below.
	default:
		g.mu.Unlock()
		return Receipt{}, fmt.Errorf("netsec: invalid challenge state %q", entry.state)
	}

	master := entry.challenge.Identity.MasterAddress
	if g.rateLimitedLocked(master, now) {
		g.mu.Unlock()
		return Receipt{}, ErrRateLimited
	}
	if err := g.verifyLocked(entry, resp); err != nil {
		g.recordFailureLocked(master, now)
		g.mu.Unlock()
		return Receipt{}, err
	}

	// Every check passed: atomically leave Pending.  Removing the sequence
	// ownership before unlocking ensures a concurrent new request cannot race
	// with an already-authorised execution.
	entry.state = StateAuthorized
	entry.done = make(chan struct{})
	delete(g.pending, entry.challenge.ChallengeID)
	g.releaseSequenceLocked(entry.challenge.Identity, entry.challenge.ChallengeID)

	challengedIdentity := entry.challenge.Identity.Clone()
	exec := Execution{
		ChallengeID:   entry.challenge.ChallengeID,
		KeyID:         entry.challenge.KeyID,
		Nonce:         entry.challenge.Nonce,
		PolicyVersion: entry.challenge.PolicyVersion,
		Identity:      challengedIdentity.Clone(),
	}
	g.mu.Unlock()

	payload, execErr := runExecutor(g.executor, exec)

	g.mu.Lock()
	receipt := Receipt{
		ChallengeID:   entry.challenge.ChallengeID,
		KeyID:         entry.challenge.KeyID,
		PolicyVersion: entry.challenge.PolicyVersion,
		Identity:      challengedIdentity.Clone(),
		Payload:       cloneBytes(payload),
		ExecutedAt:    g.nowLocked(),
	}
	if execErr != nil {
		receipt.ExecutionError = execErr.Error()
	}
	entry.receipt = &receipt
	entry.state = StateCompleted
	g.resetRateLocked(master)
	close(entry.done)
	g.mu.Unlock()
	return receipt.Clone(), nil
}

func (g *Gateway) submitLocked(id RequestIdentity, now time.Time) (*Challenge, error) {
	conn := g.connectionLocked(id.Connection())
	if conn.reset {
		return nil, ErrConnectionReset
	}

	// A different request holding the same application sequence cancels the
	// older pending challenge.  The conflicting request is not admitted.
	if oldID, occupied := conn.pending[id.ApplicationSequence]; occupied {
		if old, ok := g.challenges[oldID]; ok {
			g.cancelLocked(old, CancelSequenceOccupied)
		}
		return nil, fmt.Errorf("%w: sequence %d", ErrSequenceOccupied, id.ApplicationSequence)
	}
	if conn.haveSequence && !sequenceAfter(id.ApplicationSequence, conn.highestSeq) {
		return nil, fmt.Errorf("%w: got %d after %d", ErrSequenceReuse, id.ApplicationSequence, conn.highestSeq)
	}
	if g.maxPending >= 0 && len(conn.pending) >= g.maxPending {
		return nil, ErrTooManyPending
	}

	keyID, err := g.selectKeyLocked(id.MasterAddress)
	if err != nil {
		return nil, err
	}
	challengeID, nonce, err := g.newChallengeLocked(conn)
	if err != nil {
		return nil, err
	}

	challenge := Challenge{
		ChallengeID:   challengeID,
		KeyID:         keyID,
		Nonce:         nonce,
		Deadline:      now.Add(g.ttl),
		PolicyVersion: g.policy,
		Identity:      id.Clone(),
	}
	entry := &challengeEntry{
		challenge: challenge,
		state:     StatePending,
	}
	g.challenges[challengeID] = entry
	g.pending[challengeID] = entry
	conn.pending[id.ApplicationSequence] = challengeID
	conn.challengeIDs[challengeID] = struct{}{}
	conn.highestSeq = id.ApplicationSequence
	conn.haveSequence = true
	return &challenge, nil
}

func (g *Gateway) selectKeyLocked(master uint16) (KeyID, error) {
	if id, ok := g.keyByMaster[master]; ok {
		return id, nil
	}
	if g.defaultKey != "" {
		return g.defaultKey, nil
	}
	if len(g.keys) == 1 {
		for id := range g.keys {
			return id, nil
		}
	}
	return "", fmt.Errorf("%w: master %d", ErrKeyNotConfigured, master)
}

func (g *Gateway) newChallengeLocked(conn *connectionState) (ChallengeID, [32]byte, error) {
	var nonce [32]byte
	for attempt := 0; attempt < 256; attempt++ {
		var raw [16]byte
		if _, err := io.ReadFull(g.random, raw[:]); err != nil {
			return ChallengeID{}, nonce, fmt.Errorf("%w: %v", ErrRandomSource, err)
		}
		candidate := ChallengeID(raw)
		if _, exists := g.challenges[candidate]; exists {
			continue
		}
		if _, exists := conn.challengeIDs[candidate]; exists {
			continue
		}
		if _, err := io.ReadFull(g.random, nonce[:]); err != nil {
			return ChallengeID{}, nonce, fmt.Errorf("%w: %v", ErrRandomSource, err)
		}
		return candidate, nonce, nil
	}
	return ChallengeID{}, nonce, fmt.Errorf("%w: could not generate a unique challenge id", ErrRandomSource)
}

func (g *Gateway) verifyLocked(entry *challengeEntry, resp Response) error {
	if resp.KeyID != entry.challenge.KeyID {
		return fmt.Errorf("%w: challenge %q, response %q", ErrKeyMismatch, entry.challenge.KeyID, resp.KeyID)
	}
	secret, ok := g.keys[entry.challenge.KeyID]
	if !ok {
		return ErrKeyNotFound
	}
	expected := ComputeResponseMAC(
		secret,
		entry.challenge.Identity,
		entry.challenge.Nonce,
		entry.challenge.ChallengeID,
		entry.challenge.KeyID,
	)
	if !hmac.Equal(expected[:], resp.MAC[:]) {
		return ErrAuthenticationFailed
	}
	return nil
}

func (g *Gateway) cancelLocked(entry *challengeEntry, reason CancelReason) {
	if entry.state != StatePending {
		return
	}
	entry.state = StateCanceled
	entry.cancelReason = reason
	delete(g.pending, entry.challenge.ChallengeID)
	g.releaseSequenceLocked(entry.challenge.Identity, entry.challenge.ChallengeID)
}

func (g *Gateway) releaseSequenceLocked(id RequestIdentity, challengeID ChallengeID) {
	conn, ok := g.connections[id.Connection()]
	if !ok {
		return
	}
	seq := id.ApplicationSequence
	if cid, ok := conn.pending[seq]; ok && cid == challengeID {
		delete(conn.pending, seq)
	}
}

func (g *Gateway) expireLocked(now time.Time) int {
	canceled := 0
	for _, entry := range g.pending {
		if !now.Before(entry.challenge.Deadline) {
			g.cancelLocked(entry, CancelExpired)
			canceled++
		}
	}
	for key, a := range g.assemblies {
		if !now.Before(a.deadline) {
			delete(g.assemblies, key)
		}
	}
	return canceled
}

func (g *Gateway) connectionLocked(id ConnectionID) *connectionState {
	conn, ok := g.connections[id]
	if !ok {
		conn = &connectionState{
			pending:      make(map[uint32]ChallengeID),
			challengeIDs: make(map[ChallengeID]struct{}),
		}
		g.connections[id] = conn
	}
	return conn
}

// ResetConnection cancels every pending challenge and incomplete assembly on
// the exact connection generation and rejects further submissions that reuse
// that stale generation.
func (g *Gateway) ResetConnection(master, outstation uint16, generation uint64) int {
	id := ConnectionID{
		MasterAddress:        master,
		OutstationAddress:    outstation,
		ConnectionGeneration: generation,
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	conn := g.connectionLocked(id)
	canceled := 0
	for _, cid := range conn.pending {
		if entry, ok := g.challenges[cid]; ok {
			g.cancelLocked(entry, CancelConnectionReset)
			canceled++
		}
	}
	conn.pending = make(map[uint32]ChallengeID)
	conn.reset = true

	for key := range g.assemblies {
		if key.connection == id {
			delete(g.assemblies, key)
		}
	}
	return canceled
}

// ResetAllConnections cancels all pending challenges and assemblies.
func (g *Gateway) ResetAllConnections() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	canceled := 0
	for _, entry := range g.pending {
		g.cancelLocked(entry, CancelConnectionReset)
		canceled++
	}
	for key := range g.assemblies {
		delete(g.assemblies, key)
	}
	for _, conn := range g.connections {
		conn.reset = true
	}
	return canceled
}

// SetPolicyVersion changes the policy version.  A real change cancels every
// pending challenge and incomplete fragment assembly.  It returns the number of
// canceled pending challenges.
func (g *Gateway) SetPolicyVersion(version string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if version == g.policy {
		return 0
	}
	g.policy = version
	canceled := 0
	for _, entry := range g.pending {
		g.cancelLocked(entry, CancelPolicyChange)
		canceled++
	}
	for key := range g.assemblies {
		delete(g.assemblies, key)
	}
	return canceled
}

// PolicyVersion returns the current policy version.
func (g *Gateway) PolicyVersion() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.policy
}

// Cancel manually cancels a pending challenge.  It reports whether the
// challenge was still pending.
func (g *Gateway) Cancel(challengeID ChallengeID) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.expireLocked(g.nowLocked())
	entry, ok := g.challenges[challengeID]
	if !ok || entry.state != StatePending {
		return false
	}
	g.cancelLocked(entry, CancelManual)
	return true
}

// Expire applies deadlines and returns the number of newly canceled
// challenges.
func (g *Gateway) Expire() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.expireLocked(g.nowLocked())
}

// PendingCount returns the number of currently pending challenges.
func (g *Gateway) PendingCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.expireLocked(g.nowLocked())
	return len(g.pending)
}

// Status returns a snapshot of a challenge.
func (g *Gateway) Status(challengeID ChallengeID) (ChallengeStatus, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.expireLocked(g.nowLocked())
	entry, ok := g.challenges[challengeID]
	if !ok {
		return ChallengeStatus{}, false
	}
	return ChallengeStatus{
		State:        entry.state,
		CancelReason: entry.cancelReason,
		Deadline:     entry.challenge.Deadline,
		Identity:     entry.challenge.Identity.Clone(),
	}, true
}

// ReceiptFor returns the stored receipt for a completed challenge.
func (g *Gateway) ReceiptFor(challengeID ChallengeID) (Receipt, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	entry, ok := g.challenges[challengeID]
	if !ok || entry.receipt == nil {
		return Receipt{}, false
	}
	return entry.receipt.Clone(), true
}

func (g *Gateway) rateLimitedLocked(master uint16, now time.Time) bool {
	if g.failureLimit < 0 {
		return false
	}
	bucket, ok := g.rates[master]
	if !ok || !now.Before(bucket.windowStart.Add(g.failureWindow)) {
		return false
	}
	return bucket.failures >= g.failureLimit
}

func (g *Gateway) recordFailureLocked(master uint16, now time.Time) {
	if g.failureLimit < 0 {
		return
	}
	bucket := g.rates[master]
	if bucket == nil || !now.Before(bucket.windowStart.Add(g.failureWindow)) {
		bucket = &rateBucket{windowStart: now}
		g.rates[master] = bucket
	}
	bucket.failures++
}

func (g *Gateway) resetRateLocked(master uint16) {
	delete(g.rates, master)
}

func (g *Gateway) nowLocked() time.Time {
	now := g.now()
	if g.lastNow.IsZero() || now.After(g.lastNow) {
		g.lastNow = now
	}
	return g.lastNow
}

func runExecutor(exec Executor, execution Execution) (payload []byte, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("operation callback panicked: %v", recovered)
		}
	}()
	return exec(execution)
}

func cloneChallenge(in *Challenge) *Challenge {
	if in == nil {
		return nil
	}
	out := *in
	out.Identity = in.Identity.Clone()
	return &out
}

func sequenceAfter(candidate, previous uint32) bool {
	return int32(candidate-previous) > 0
}
