package netsec

import (
	"crypto/subtle"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// DefaultMaxIncomplete is the soft threshold for incomplete SAE
	// transactions. Above it, a commit without a valid anti-clogging token
	// receives only a token.
	DefaultMaxIncomplete = 64
	// DefaultMaxIncompletePerStation bounds one station's share of pending
	// and admitted transactions.
	DefaultMaxIncompletePerStation = 4
	// DefaultTransactionTTL is how long an admitted transaction remains in
	// the incomplete count without Complete or Abort.
	DefaultTransactionTTL = 2 * time.Minute
	// DefaultFailureTTL is how long a failed expensive validation stays in
	// the pressure count that triggers anti-clogging tokens.
	DefaultFailureTTL = time.Minute
	// DefaultMaxConsumedTokens bounds the replay cache.
	DefaultMaxConsumedTokens = 65536
)

// Action is the Authentication handler's next action.
type Action uint8

const (
	// ActionDrop discards the frame without a response.
	ActionDrop Action = iota
	// ActionSendToken sends only an anti-clogging token. It must not start
	// SAE processing or curve arithmetic.
	ActionSendToken
	// ActionProcess hands the commit to the SAE state machine.
	ActionProcess
)

const (
	// ActionToken is an alias for ActionSendToken.
	ActionToken = ActionSendToken
	// ActionAllow is an alias for ActionProcess.
	ActionAllow = ActionProcess
)

func (a Action) String() string {
	switch a {
	case ActionDrop:
		return "drop"
	case ActionSendToken:
		return "send-token"
	case ActionProcess:
		return "process"
	default:
		return fmt.Sprintf("action(%d)", uint8(a))
	}
}

// Decision tells the Authentication frame handler what to do with a commit.
type Decision struct {
	Action Action
	// Token is non-empty only for ActionSendToken.
	Token []byte
	// Reason explains a token challenge or a drop.
	Reason error
	// Duplicate means the same commit is already part of an active
	// transaction. The handler should retransmit the stored SAE response
	// rather than run curve arithmetic again.
	Duplicate bool
}

// Allowed reports whether the commit may enter the SAE state machine.
func (d Decision) Allowed() bool { return d.Action == ActionProcess }

func (d Decision) String() string {
	switch d.Action {
	case ActionSendToken:
		return fmt.Sprintf("send-token (%v)", d.Reason)
	case ActionDrop:
		return fmt.Sprintf("drop (%v)", d.Reason)
	case ActionProcess:
		if d.Duplicate {
			return "process (duplicate)"
		}
		return "process"
	default:
		return d.Action.String()
	}
}

// Config configures a Filter. Zero-valued fields select secure defaults.
type Config struct {
	// MaxIncomplete is the soft threshold. Once this many transactions are
	// incomplete, commits without a token receive only a fresh token.
	MaxIncomplete int
	// MaxIncompletePerStation bounds active plus pending transactions for
	// one station MAC.
	MaxIncompletePerStation int
	// MaxTransactions is the hard active-plus-pending cap. Token-bearing
	// commits may exceed MaxIncomplete but never this cap. Zero selects
	// four times MaxIncomplete.
	MaxTransactions int
	// TransactionTTL removes abandoned admitted transactions from the
	// incomplete count.
	TransactionTTL time.Duration
	// FailureTTL is how long an invalid commit that required a curve
	// validation continues to count as pressure. Zero selects
	// DefaultFailureTTL.
	FailureTTL time.Duration
	// TokenSecret is the master secret for token-key rotation. An empty
	// value generates a random secret.
	TokenSecret []byte
	// TokenRotation is the anti-clogging key rotation interval.
	TokenRotation time.Duration
	// TokenOverlap is the grace period for tokens in the preceding key epoch.
	TokenOverlap time.Duration
	// Clock overrides the time source, primarily for tests.
	Clock func() time.Time
	// OwnElements maps a group to the AP's current public element.
	OwnElements map[GroupID][]byte
}

type transactionKey struct {
	station string
	group   GroupID
}

type transaction struct {
	fingerprint [32]byte
	admitted    time.Time
}

type pendingTransaction struct {
	fingerprint [32]byte
}

// Stats are monotonic counters useful for observability and tests.
type Stats struct {
	CurveValidations    uint64
	TokensIssued        uint64
	Admitted            uint64
	Dropped             uint64
	DuplicateAdmissions uint64
}

// Filter is the SAE Authentication frame pre-filter.
type Filter struct {
	maxIncomplete   int
	maxPerStation   int
	maxTransactions int
	transactionTTL  time.Duration
	failureTTL      time.Duration
	now             func() time.Time
	tokens          *TokenManager

	mu          sync.Mutex
	active      map[transactionKey]transaction
	pending     map[transactionKey]pendingTransaction
	stationOwn  map[string]int
	consumed    map[string]time.Time
	ownElements map[GroupID][]byte
	maxConsumed int
	// recentFailures records expensive invalid attempts. It prevents a
	// flood of off-curve points from repeatedly refreshing the free
	// no-token validation budget.
	recentFailures []time.Time

	curveValidations    atomic.Uint64
	tokensIssued        atomic.Uint64
	admitted            atomic.Uint64
	dropped             atomic.Uint64
	duplicateAdmissions atomic.Uint64
}

// NewFilter constructs a pre-filter.
func NewFilter(cfg Config) (*Filter, error) {
	if cfg.MaxIncomplete < 0 || cfg.MaxIncompletePerStation < 0 || cfg.MaxTransactions < 0 {
		return nil, fmt.Errorf("netsec: negative transaction limit")
	}
	if cfg.TransactionTTL < 0 {
		return nil, fmt.Errorf("netsec: negative transaction TTL")
	}
	if cfg.FailureTTL < 0 {
		return nil, fmt.Errorf("netsec: negative failure TTL")
	}

	maxIncomplete := cfg.MaxIncomplete
	if maxIncomplete == 0 {
		maxIncomplete = DefaultMaxIncomplete
	}
	maxPerStation := cfg.MaxIncompletePerStation
	if maxPerStation == 0 {
		maxPerStation = DefaultMaxIncompletePerStation
	}
	maxTransactions := cfg.MaxTransactions
	if maxTransactions == 0 {
		maxTransactions = maxIncomplete * 4
	}
	if maxTransactions < maxIncomplete {
		return nil, fmt.Errorf("netsec: MaxTransactions %d is below MaxIncomplete %d", maxTransactions, maxIncomplete)
	}
	ttl := cfg.TransactionTTL
	if ttl == 0 {
		ttl = DefaultTransactionTTL
	}
	failureTTL := cfg.FailureTTL
	if failureTTL == 0 {
		failureTTL = DefaultFailureTTL
	}

	now := cfg.Clock
	if now == nil {
		now = time.Now
	}

	opts := []TokenOption{WithClock(now)}
	if cfg.TokenOverlap > 0 {
		opts = append(opts, WithOverlap(cfg.TokenOverlap))
	}
	tokens, err := NewTokenManager(cfg.TokenSecret, cfg.TokenRotation, opts...)
	if err != nil {
		return nil, err
	}

	own := make(map[GroupID][]byte, len(cfg.OwnElements))
	for id, element := range cfg.OwnElements {
		g, ok := LookupGroup(id)
		if !ok {
			return nil, ErrUnsupportedGroup
		}
		canonical, err := g.CanonicalElement(element)
		if err != nil {
			return nil, fmt.Errorf("%w: group %d: %v", ErrOwnElement, id, err)
		}
		own[id] = canonical
	}

	return &Filter{
		maxIncomplete:   maxIncomplete,
		maxPerStation:   maxPerStation,
		maxTransactions: maxTransactions,
		transactionTTL:  ttl,
		failureTTL:      failureTTL,
		now:             now,
		tokens:          tokens,
		active:          make(map[transactionKey]transaction),
		pending:         make(map[transactionKey]pendingTransaction),
		stationOwn:      make(map[string]int),
		consumed:        make(map[string]time.Time),
		ownElements:     own,
		maxConsumed:     DefaultMaxConsumedTokens,
	}, nil
}

// New is a convenience constructor using the default token settings.
func New(maxIncomplete int, tokenSecret []byte) (*Filter, error) {
	return NewFilter(Config{
		MaxIncomplete: maxIncomplete,
		TokenSecret:   tokenSecret,
	})
}

// HandleCommit is the pre-filter entry point for an SAE Commit frame.
func (f *Filter) HandleCommit(req CommitRequest) Decision {
	if err := validateStationMAC(req.StationMAC); err != nil {
		return f.reject(err)
	}
	g, ok := LookupGroup(req.Group)
	if !ok {
		return f.reject(ErrUnsupportedGroup)
	}

	now := f.now()
	key := transactionKey{station: string(req.StationMAC), group: req.Group}
	fingerprint := commitFingerprint(req.Group, req.Scalar, req.Element)

	f.mu.Lock()
	f.cleanupLocked(now)
	if tx, ok := f.active[key]; ok {
		f.mu.Unlock()
		if tx.fingerprint == fingerprint {
			f.duplicateAdmissions.Add(1)
			return Decision{Action: ActionProcess, Duplicate: true}
		}
		return f.reject(ErrTransactionInProgress)
	}
	if _, ok := f.pending[key]; ok {
		f.mu.Unlock()
		return f.reject(ErrTransactionInProgress)
	}
	globalCount := len(f.active) + len(f.pending) + len(f.recentFailures)
	stationCount := f.stationOwn[string(req.StationMAC)]
	f.mu.Unlock()

	if len(req.Token) > 0 {
		if !f.tokens.Verify(req.StationMAC, req.Group, req.Token) {
			return f.challenge(req.StationMAC, req.Group, ErrTokenInvalid)
		}

		// Scalar validation is cheap. Do it before reserving or consuming
		// the token so a malformed scalar does not burn a token.
		if err := g.ValidateScalar(req.Scalar); err != nil {
			return f.reject(err)
		}

		f.mu.Lock()
		f.cleanupLocked(now)
		if tx, ok := f.active[key]; ok {
			f.mu.Unlock()
			if tx.fingerprint == fingerprint {
				f.duplicateAdmissions.Add(1)
				return Decision{Action: ActionProcess, Duplicate: true}
			}
			return f.reject(ErrTransactionInProgress)
		}
		if _, ok := f.pending[key]; ok {
			f.mu.Unlock()
			return f.reject(ErrTransactionInProgress)
		}
		if exp, used := f.consumed[string(req.Token)]; used {
			if now.Before(exp) {
				f.mu.Unlock()
				return f.challenge(req.StationMAC, req.Group, ErrTokenReplay)
			}
			delete(f.consumed, string(req.Token))
		}
		globalCount = len(f.active) + len(f.pending)
		stationCount = f.stationOwn[string(req.StationMAC)]
		if globalCount >= f.maxTransactions {
			f.mu.Unlock()
			return f.reject(ErrTransactionCapacity)
		}
		if stationCount >= f.maxPerStation {
			f.mu.Unlock()
			return f.reject(ErrStationTransactionLimit)
		}

		f.reserveLocked(key, req.StationMAC, fingerprint)
		// A token authorizes one expensive validation attempt. Consume it
		// even if the point turns out to be invalid.
		f.consumed[string(req.Token)] = f.tokens.Expiry(now)
		f.mu.Unlock()

		admitted := false
		defer func() {
			if !admitted {
				f.releasePending(key, req.StationMAC, fingerprint)
			}
		}()

		checked, err := f.validateCommit(g, req)
		if err != nil {
			if checked {
				f.recordFailure(now)
			}
			return f.reject(err)
		}
		f.promote(key, fingerprint, now)
		admitted = true
		f.admitted.Add(1)
		return Decision{Action: ActionProcess}
	}

	// No token. Hard-gated requests get only a token and never reach curve
	// validation. The check also counts reservations in progress, so a burst
	// of concurrent frames cannot exceed the soft threshold.
	if globalCount >= f.maxIncomplete {
		return f.challenge(req.StationMAC, req.Group, ErrTransactionThreshold)
	}

	f.mu.Lock()
	f.cleanupLocked(now)
	if tx, ok := f.active[key]; ok {
		f.mu.Unlock()
		if tx.fingerprint == fingerprint {
			f.duplicateAdmissions.Add(1)
			return Decision{Action: ActionProcess, Duplicate: true}
		}
		return f.reject(ErrTransactionInProgress)
	}
	if _, ok := f.pending[key]; ok {
		f.mu.Unlock()
		return f.reject(ErrTransactionInProgress)
	}
	stationCount = f.stationOwn[string(req.StationMAC)]
	if stationCount >= f.maxPerStation {
		f.mu.Unlock()
		return f.reject(ErrStationTransactionLimit)
	}
	if len(f.active)+len(f.pending)+len(f.recentFailures) >= f.maxIncomplete {
		f.mu.Unlock()
		return f.challenge(req.StationMAC, req.Group, ErrTransactionThreshold)
	}
	f.reserveLocked(key, req.StationMAC, fingerprint)
	f.mu.Unlock()

	admitted := false
	defer func() {
		if !admitted {
			f.releasePending(key, req.StationMAC, fingerprint)
		}
	}()

	checked, err := f.validateCommit(g, req)
	if err != nil {
		if checked {
			f.recordFailure(now)
		}
		return f.reject(err)
	}
	f.promote(key, fingerprint, now)
	admitted = true
	f.admitted.Add(1)
	return Decision{Action: ActionProcess}
}

// HandleAuthentication is an alias for HandleCommit.
func (f *Filter) HandleAuthentication(req AuthenticationRequest) Decision {
	return f.HandleCommit(req)
}

// ProcessCommit is an alias for HandleCommit.
func (f *Filter) ProcessCommit(req CommitRequest) Decision {
	return f.HandleCommit(req)
}

// AntiCloggingToken issues a token bound to the station MAC and SAE group.
func (f *Filter) AntiCloggingToken(mac net.HardwareAddr, group GroupID) ([]byte, error) {
	if err := validateStationMAC(mac); err != nil {
		return nil, err
	}
	if _, ok := LookupGroup(group); !ok {
		return nil, ErrUnsupportedGroup
	}
	token := f.tokens.Token(mac, group)
	if token == nil {
		return nil, ErrTokenUnavailable
	}
	f.tokensIssued.Add(1)
	return append([]byte(nil), token...), nil
}

// Token is an alias for AntiCloggingToken.
func (f *Filter) Token(mac net.HardwareAddr, group GroupID) ([]byte, error) {
	return f.AntiCloggingToken(mac, group)
}

// VerifyToken checks a token without consuming it.
func (f *Filter) VerifyToken(mac net.HardwareAddr, group GroupID, token []byte) bool {
	return f.tokens.Verify(mac, group, token)
}

// Complete removes an admitted transaction from the incomplete count. The
// Authentication handler calls it after Confirm succeeds or the exchange is
// otherwise finished.
func (f *Filter) Complete(mac net.HardwareAddr, group GroupID) bool {
	if len(mac) != 6 {
		return false
	}
	key := transactionKey{station: string(mac), group: group}

	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.active[key]; !ok {
		return false
	}
	delete(f.active, key)
	f.decrementStationLocked(mac)
	return true
}

// Abort is an alias for Complete.
func (f *Filter) Abort(mac net.HardwareAddr, group GroupID) bool {
	return f.Complete(mac, group)
}

// CompleteStation removes all admitted transactions for a station and returns
// the number removed.
func (f *Filter) CompleteStation(mac net.HardwareAddr) int {
	if len(mac) != 6 {
		return 0
	}
	station := string(mac)

	f.mu.Lock()
	defer f.mu.Unlock()
	removed := 0
	for key := range f.active {
		if key.station == station {
			delete(f.active, key)
			removed++
		}
	}
	if removed > 0 {
		f.stationOwn[station] -= removed
		if f.stationOwn[station] <= 0 {
			delete(f.stationOwn, station)
		}
	}
	return removed
}

// Incomplete returns the number of admitted transactions awaiting completion.
func (f *Filter) Incomplete() int {
	now := f.now()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleanupLocked(now)
	return len(f.active)
}

// Pressure returns the value compared with MaxIncomplete: admitted
// transactions, in-flight admissions, and recent expensive failures.
func (f *Filter) Pressure() int {
	now := f.now()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleanupLocked(now)
	return len(f.active) + len(f.pending) + len(f.recentFailures)
}

// IncompleteFor returns the admitted transaction count for a station.
func (f *Filter) IncompleteFor(mac net.HardwareAddr) int {
	if len(mac) != 6 {
		return 0
	}
	station := string(mac)
	now := f.now()

	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleanupLocked(now)
	count := 0
	for key := range f.active {
		if key.station == station {
			count++
		}
	}
	return count
}

// SetOwnElement configures the AP's public element for reflection rejection.
// Passing nil clears the value.
func (f *Filter) SetOwnElement(group GroupID, element []byte) error {
	g, ok := LookupGroup(group)
	if !ok {
		return ErrUnsupportedGroup
	}
	if len(element) == 0 {
		f.mu.Lock()
		delete(f.ownElements, group)
		f.mu.Unlock()
		return nil
	}
	canonical, err := g.CanonicalElement(element)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrOwnElement, err)
	}
	f.mu.Lock()
	f.ownElements[group] = canonical
	f.mu.Unlock()
	return nil
}

// OwnElement returns the configured AP element for a group.
func (f *Filter) OwnElement(group GroupID) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.ownElements[group]...)
}

// Stats returns a snapshot of the filter counters.
func (f *Filter) Stats() Stats {
	return Stats{
		CurveValidations:    f.curveValidations.Load(),
		TokensIssued:        f.tokensIssued.Load(),
		Admitted:            f.admitted.Load(),
		Dropped:             f.dropped.Load(),
		DuplicateAdmissions: f.duplicateAdmissions.Load(),
	}
}

// validateCommit returns checked=true when it performed element (curve) work.
// A scalar encoding failure is rejected before any expensive work.
func (f *Filter) validateCommit(g *Group, req CommitRequest) (bool, error) {
	if err := g.ValidateScalar(req.Scalar); err != nil {
		return false, err
	}

	f.curveValidations.Add(1)
	element, err := g.CanonicalElement(req.Element)
	if err != nil {
		return true, err
	}

	var own []byte
	if len(req.APElement) > 0 {
		own, err = g.CanonicalElement(req.APElement)
		if err != nil {
			return true, fmt.Errorf("%w: %v", ErrOwnElement, err)
		}
	} else {
		f.mu.Lock()
		own = f.ownElements[g.ID()]
		f.mu.Unlock()
	}
	if len(own) > 0 && subtle.ConstantTimeCompare(element, own) == 1 {
		return true, ErrReflection
	}
	return true, nil
}

func (f *Filter) recordFailure(now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recentFailures = append(f.recentFailures, now)
	// The soft threshold keeps failed attempts bounded in normal operation.
	// This cap also bounds memory when token-bearing invalid attempts are
	// used to bypass the soft threshold.
	if len(f.recentFailures) > f.maxTransactions {
		f.recentFailures = f.recentFailures[len(f.recentFailures)-f.maxTransactions:]
	}
}

func (f *Filter) challenge(mac net.HardwareAddr, group GroupID, cause error) Decision {
	token, err := f.AntiCloggingToken(mac, group)
	if err != nil {
		return f.reject(fmt.Errorf("%w: %v", ErrTokenUnavailable, err))
	}
	return Decision{Action: ActionSendToken, Token: token, Reason: cause}
}

func (f *Filter) reject(err error) Decision {
	f.dropped.Add(1)
	return Decision{Action: ActionDrop, Reason: err}
}

func (f *Filter) reserveLocked(key transactionKey, mac net.HardwareAddr, fingerprint [32]byte) {
	f.pending[key] = pendingTransaction{fingerprint: fingerprint}
	f.stationOwn[string(mac)]++
}

func (f *Filter) releasePending(key transactionKey, mac net.HardwareAddr, fingerprint [32]byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pending, ok := f.pending[key]
	if !ok || pending.fingerprint != fingerprint {
		return
	}
	delete(f.pending, key)
	f.decrementStationLocked(mac)
}

func (f *Filter) promote(key transactionKey, fingerprint [32]byte, now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.pending, key)
	f.active[key] = transaction{fingerprint: fingerprint, admitted: now}
}

func (f *Filter) decrementStationLocked(mac net.HardwareAddr) {
	station := string(mac)
	f.stationOwn[station]--
	if f.stationOwn[station] <= 0 {
		delete(f.stationOwn, station)
	}
}

func (f *Filter) cleanupLocked(now time.Time) {
	if f.transactionTTL > 0 {
		for key, tx := range f.active {
			if now.Sub(tx.admitted) > f.transactionTTL {
				delete(f.active, key)
				f.decrementStationLocked([]byte(key.station))
			}
		}
	}

	if f.failureTTL > 0 {
		first := 0
		for first < len(f.recentFailures) && now.Sub(f.recentFailures[first]) > f.failureTTL {
			first++
		}
		if first > 0 {
			f.recentFailures = f.recentFailures[first:]
		}
	}

	for token, expiry := range f.consumed {
		if !now.Before(expiry) {
			delete(f.consumed, token)
		}
	}
	if len(f.consumed) > f.maxConsumed {
		target := f.maxConsumed / 2
		for token := range f.consumed {
			delete(f.consumed, token)
			if len(f.consumed) <= target {
				break
			}
		}
	}
}

func validateStationMAC(mac net.HardwareAddr) error {
	if len(mac) != 6 {
		return ErrInvalidStation
	}
	return nil
}
