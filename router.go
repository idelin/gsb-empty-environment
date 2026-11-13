package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net/netip"
	"sort"
	"sync"
	"time"
)

// StalePolicy controls route validity once a committed view exceeds MaxStale.
type StalePolicy string

const (
	PolicyFailClosed StalePolicy = "fail-closed"
	PolicyUnknown    StalePolicy = "unknown"

	FailClosedPolicy = PolicyFailClosed
	UnknownPolicy    = PolicyUnknown
	StaleFailClosed  = PolicyFailClosed
	StaleUnknown     = PolicyUnknown
)

func (p StalePolicy) valid() bool {
	return p == "" || p == PolicyFailClosed || p == PolicyUnknown
}

// RouterConfig is host configuration. Zero durations select conservative
// defaults; tests and hosts can override them explicitly.
type RouterConfig struct {
	TrustedKeys  map[CacheID]ed25519.PublicKey
	StaleAfter   time.Duration
	MaxStale     time.Duration
	StalePolicy  StalePolicy
	Preference   []CacheID
	Now          func() time.Time
	MaxFragments uint32
}

const (
	defaultStaleAfter   = 15 * time.Minute
	defaultMaxStale     = 24 * time.Hour
	defaultMaxFragments = 1 << 20
)

// ViewStatus is the freshness of a committed view.
type ViewStatus string

const (
	ViewFresh   ViewStatus = "fresh"
	ViewStale   ViewStatus = "stale"
	ViewExpired ViewStatus = "expired"
	ViewUnknown ViewStatus = "unknown"
)

// View is an immutable, coherent copy of one cache's committed state. It is
// never assembled from a mixture of old and new sets.
type View struct {
	CacheID    CacheID
	Session    SessionID
	Serial     uint32
	RootDigest Digest
	VRPs       []VRP
	UpdatedAt  time.Time
	StaleFor   time.Duration
	Status     ViewStatus
}

// ValidationStatus is the routing decision for one route.
type ValidationStatus string

const (
	StatusValid   ValidationStatus = "valid"
	StatusInvalid ValidationStatus = "invalid"
	StatusUnknown ValidationStatus = "unknown"
	StatusStale   ValidationStatus = "stale"
)

// Validation contains the decision plus the matching VRPs from the committed
// view. A stale result is deliberately not reported as valid.
type Validation struct {
	Status  ValidationStatus
	Matches []VRP
	View    View
}

func (v Validation) Valid() bool { return v.Status == StatusValid }

type committedView struct {
	session   SessionID
	serial    uint32
	root      Digest
	set       map[string]vrpRecord
	sorted    []vrpRecord
	updatedAt time.Time
}

func (v *committedView) copyVRPs() []VRP {
	out := make([]VRP, len(v.sorted))
	for i := range v.sorted {
		out[i] = v.sorted[i].vrp
	}
	return out
}

type pendingResponse struct {
	queryKind QueryKind
	session   SessionID
	from      uint32
	to        uint32
	total     uint32
	parts     map[uint32]SignedMessage
}

type routerCache struct {
	cacheID string
	key     ed25519.PublicKey

	generation   uint64
	connected    bool
	activeReq    uint64
	activeKind   QueryKind
	activeFrom   uint32
	responseDone bool
	pending      *pendingResponse

	committed *committedView
	retired   map[SessionID]struct{}
	markStale bool
}

// Router validates and commits cache responses. Each cache ID has independent
// generation, pending response, session history and view state.
type Router struct {
	mu         sync.RWMutex
	stores     map[CacheID]*routerCache
	staleAfter time.Duration
	maxStale   time.Duration
	policy     StalePolicy
	preference []CacheID
	now        func() time.Time
	maxParts   uint32
}

// NewRouter constructs a router and validates its trusted key registry.
func NewRouter(config RouterConfig) (*Router, error) {
	if !config.StalePolicy.valid() {
		return nil, fmt.Errorf("%w: %q", ErrInvalidPolicy, config.StalePolicy)
	}
	if config.StalePolicy == "" {
		config.StalePolicy = PolicyFailClosed
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	maxStaleConfigured := config.MaxStale > 0
	if !maxStaleConfigured {
		config.MaxStale = defaultMaxStale
	}
	if config.StaleAfter <= 0 {
		config.StaleAfter = defaultStaleAfter
		if maxStaleConfigured && config.MaxStale < config.StaleAfter {
			config.StaleAfter = config.MaxStale
		}
	}
	if config.MaxStale < config.StaleAfter {
		config.MaxStale = config.StaleAfter
	}
	if config.MaxFragments == 0 {
		config.MaxFragments = defaultMaxFragments
	}

	r := &Router{
		stores:     make(map[CacheID]*routerCache),
		staleAfter: config.StaleAfter,
		maxStale:   config.MaxStale,
		policy:     config.StalePolicy,
		preference: append([]CacheID(nil), config.Preference...),
		now:        config.Now,
		maxParts:   config.MaxFragments,
	}
	for cacheID, key := range config.TrustedKeys {
		if err := r.AddCache(cacheID, key); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// MustNewRouter is NewRouter for static host configuration.
func MustNewRouter(config RouterConfig) *Router {
	r, err := NewRouter(config)
	if err != nil {
		panic(err)
	}
	return r
}

// AddCache registers or rotates the trusted Ed25519 key for a cache. Existing
// committed state is preserved and all in-flight responses are discarded.
func (r *Router) AddCache(cacheID CacheID, publicKey ed25519.PublicKey) error {
	if cacheID == "" {
		return fmt.Errorf("netsec: empty cache ID")
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: public key is %d bytes", ErrCacheNotTrusted, len(publicKey))
	}
	key := append(ed25519.PublicKey(nil), publicKey...)
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.stores[cacheID]
	if state == nil {
		state = &routerCache{
			cacheID: cacheID,
			retired: make(map[SessionID]struct{}),
		}
		r.stores[cacheID] = state
	}
	state.key = key
	state.pending = nil
	state.responseDone = false
	state.activeReq = 0
	return nil
}

// RegisterCache is an alias for AddCache.
func (r *Router) RegisterCache(cacheID CacheID, publicKey ed25519.PublicKey) error {
	return r.AddCache(cacheID, publicKey)
}

// BeginConnection starts a new transport generation for one cache. Any
// partial response is abandoned; the committed view remains available.
func (r *Router) BeginConnection(cacheID CacheID) (uint64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, err := r.lookupLocked(cacheID)
	if err != nil {
		return 0, err
	}
	next, err := incrementGeneration(state.generation)
	if err != nil {
		return 0, err
	}
	state.generation = next
	state.connected = true
	state.pending = nil
	state.responseDone = false
	state.activeReq = 0
	return state.generation, nil
}

// Disconnect abandons an in-flight response. It does not alter the committed
// view or the current generation; BeginConnection performs the next rotation.
func (r *Router) Disconnect(cacheID CacheID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, err := r.lookupLocked(cacheID)
	if err != nil {
		return err
	}
	state.connected = false
	state.pending = nil
	state.responseDone = false
	state.activeReq = 0
	return nil
}

// ResetQuery creates a Reset Query bound to the current connection.
func (r *Router) ResetQuery(cacheID CacheID) (Query, error) {
	return r.newQuery(cacheID, QueryReset)
}

// SerialQuery creates a Serial Query starting at the committed serial.
func (r *Router) SerialQuery(cacheID CacheID) (Query, error) {
	return r.newQuery(cacheID, QuerySerial)
}

// NextQuery chooses Reset when no complete view exists and Serial otherwise.
func (r *Router) NextQuery(cacheID CacheID) (Query, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, err := r.lookupLocked(cacheID)
	if err != nil {
		return Query{}, err
	}
	if state.committed == nil || state.markStale {
		return r.newQueryLocked(state, QueryReset)
	}
	return r.newQueryLocked(state, QuerySerial)
}

func (r *Router) newQuery(cacheID CacheID, kind QueryKind) (Query, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	state, err := r.lookupLocked(cacheID)
	if err != nil {
		return Query{}, err
	}
	return r.newQueryLocked(state, kind)
}

func (r *Router) newQueryLocked(state *routerCache, kind QueryKind) (Query, error) {
	if kind == QuerySerial && (state.committed == nil || state.markStale) {
		return Query{}, ErrNeedReset
	}
	if !state.connected {
		next, err := incrementGeneration(state.generation)
		if err != nil {
			return Query{}, err
		}
		state.generation = next
		state.connected = true
	}
	requestID, err := randomRequestID()
	if err != nil {
		return Query{}, err
	}
	state.pending = nil
	state.responseDone = false
	state.activeReq = requestID
	state.activeKind = kind
	state.activeFrom = 0
	if kind == QuerySerial {
		state.activeFrom = state.committed.serial
	}
	return Query{
		CacheID:              state.cacheID,
		ConnectionGeneration: state.generation,
		RequestID:            requestID,
		Kind:                 kind,
		FromSerial:           state.activeFrom,
	}, nil
}

func incrementGeneration(current uint64) (uint64, error) {
	if current == ^uint64(0) {
		return 0, fmt.Errorf("%w: connection generation overflow", ErrInvalidState)
	}
	return current + 1, nil
}

func randomRequestID() (uint64, error) {
	for {
		var buf [8]byte
		if _, err := rand.Read(buf[:]); err != nil {
			return 0, fmt.Errorf("netsec: generate request ID: %w", err)
		}
		if id := binary.BigEndian.Uint64(buf[:]); id != 0 {
			return id, nil
		}
	}
}

// IngestResponse feeds one complete response. Fragments may be presented out
// of order; they are buffered and committed together. An over-the-wire stream
// that is delivered in several batches should use IngestMessage instead.
func (r *Router) IngestResponse(messages []SignedMessage) error {
	if len(messages) == 0 {
		return nil
	}
	for i := range messages {
		if err := r.IngestMessage(messages[i]); err != nil {
			return err
		}
	}
	r.mu.RLock()
	state := r.stores[messages[0].CacheID]
	incomplete := state != nil && state.pending != nil && state.activeReq == messages[0].RequestID
	r.mu.RUnlock()
	if incomplete {
		return fmt.Errorf("%w: response has not delivered every fragment", ErrIncompleteResponse)
	}
	return nil
}

// ApplyResponse is an alias for IngestResponse.
func (r *Router) ApplyResponse(messages []SignedMessage) error {
	return r.IngestResponse(messages)
}

// IngestMessage verifies and stages one signed fragment. No state visible to
// readers changes until the response is complete and all roots have matched.
func (r *Router) IngestMessage(message SignedMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	state, err := r.lookupLocked(message.CacheID)
	if err != nil {
		return err
	}
	if err := VerifyMessage(state.key, message); err != nil {
		return err
	}
	if !state.connected || message.ConnectionGeneration != state.generation {
		return fmt.Errorf("%w: message generation %d, current %d", ErrConnectionGeneration, message.ConnectionGeneration, state.generation)
	}
	if state.activeReq == 0 || message.RequestID != state.activeReq {
		return fmt.Errorf("%w: message request %d, active %d", ErrUnexpectedResponse, message.RequestID, state.activeReq)
	}
	if state.responseDone {
		return ErrResponseReplay
	}
	if message.Total == 0 || message.Total > r.maxParts || message.Index >= message.Total {
		return fmt.Errorf("%w: fragment %d/%d", ErrMalformedMessage, message.Index, message.Total)
	}
	matchesKind := (state.activeKind == QueryReset && message.Kind.isReset()) ||
		(state.activeKind == QuerySerial && message.Kind.isDelta())
	if !matchesKind {
		return fmt.Errorf("%w: message kind %s for %s query", ErrMalformedMessage, message.Kind, state.activeKind)
	}
	if err := validateFragmentPosition(message); err != nil {
		return err
	}

	if state.pending == nil {
		state.pending = &pendingResponse{
			queryKind: state.activeKind,
			session:   message.Session,
			from:      message.FromSerial,
			to:        message.ToSerial,
			total:     message.Total,
			parts:     make(map[uint32]SignedMessage, message.Total),
		}
		if state.activeKind == QuerySerial && message.FromSerial != state.activeFrom {
			state.pending = nil
			return fmt.Errorf("%w: response starts at %d, query starts at %d", ErrUnexpectedResponse, message.FromSerial, state.activeFrom)
		}
		if state.activeKind == QueryReset &&
			state.committed != nil &&
			message.Session != state.committed.session {
			if _, retired := state.retired[message.Session]; retired {
				state.pending = nil
				return ErrSessionRetired
			}
			state.markStale = true
		}
	} else {
		pending := state.pending
		if pending.total != message.Total {
			return fmt.Errorf("%w: fragment total changed from %d to %d", ErrMalformedMessage, pending.total, message.Total)
		}
		if pending.session != message.Session {
			return ErrSessionMismatch
		}
		if pending.from != message.FromSerial || pending.to != message.ToSerial {
			return fmt.Errorf("%w: response bounds changed", ErrMalformedMessage)
		}
		if state.activeKind == QueryReset && message.Serial != pending.to {
			return fmt.Errorf("%w: reset fragment serial changed", ErrMalformedMessage)
		}
	}

	if existing, exists := state.pending.parts[message.Index]; exists {
		if !messageEqual(existing, message) {
			return ErrDuplicateMessage
		}
		return nil
	}
	state.pending.parts[message.Index] = message.copy()
	if uint32(len(state.pending.parts)) != state.pending.total {
		return nil
	}

	pending := state.pending
	state.pending = nil
	state.responseDone = true
	switch pending.queryKind {
	case QueryReset:
		err = r.completeResetLocked(state, pending)
	case QuerySerial:
		err = r.completeDeltaLocked(state, pending)
	default:
		err = fmt.Errorf("%w: pending query kind %d", ErrInvalidState, pending.queryKind)
	}
	if err != nil {
		return err
	}
	return nil
}

func validateFragmentPosition(message SignedMessage) error {
	switch message.Kind {
	case MessageResetBegin:
		if message.Index != 0 {
			return fmt.Errorf("%w: reset begin index is %d", ErrMalformedMessage, message.Index)
		}
	case MessageResetEnd:
		if message.Index != message.Total-1 {
			return fmt.Errorf("%w: reset end index is %d", ErrMalformedMessage, message.Index)
		}
	case MessageResetVRP:
		if message.Total < 3 || message.Index == 0 || message.Index >= message.Total-1 {
			return fmt.Errorf("%w: reset VRP index is %d of %d", ErrMalformedMessage, message.Index, message.Total)
		}
	case MessageSerialEnd:
		if message.Index != message.Total-1 {
			return fmt.Errorf("%w: serial end index is %d", ErrMalformedMessage, message.Index)
		}
	case MessageAdd, MessageRemove:
		if message.Index == message.Total-1 {
			return fmt.Errorf("%w: delta event occupies the end slot", ErrMalformedMessage)
		}
	default:
		return fmt.Errorf("%w: unknown message kind", ErrMalformedMessage)
	}
	return nil
}

func (r *Router) completeResetLocked(state *routerCache, pending *pendingResponse) error {
	if pending.total < 2 {
		return fmt.Errorf("%w: reset needs begin and end", ErrIncompleteResponse)
	}
	begin, ok := pending.parts[0]
	if !ok || begin.Kind != MessageResetBegin {
		return fmt.Errorf("%w: missing reset begin", ErrIncompleteResponse)
	}
	end, ok := pending.parts[pending.total-1]
	if !ok || end.Kind != MessageResetEnd {
		return fmt.Errorf("%w: missing reset end", ErrIncompleteResponse)
	}
	if begin.Session.IsZero() {
		return fmt.Errorf("%w: zero session", ErrMalformedMessage)
	}
	if begin.Session != end.Session || begin.Serial != end.Serial || begin.FromSerial != 0 || end.FromSerial != 0 {
		return ErrSessionMismatch
	}
	if begin.ToSerial != begin.Serial || end.ToSerial != end.Serial || end.Serial != end.ToSerial {
		return fmt.Errorf("%w: inconsistent reset bounds", ErrMalformedMessage)
	}

	var vrps []VRP
	for index := uint32(1); index < pending.total-1; index++ {
		msg, exists := pending.parts[index]
		if !exists || msg.Kind != MessageResetVRP {
			return fmt.Errorf("%w: missing VRP fragment %d", ErrIncompleteResponse, index)
		}
		if msg.Session != begin.Session || msg.Serial != begin.Serial ||
			msg.FromSerial != 0 || msg.ToSerial != begin.Serial {
			return fmt.Errorf("%w: VRP fragment disagrees with begin", ErrSessionMismatch)
		}
		vrps = append(vrps, msg.VRP)
	}
	set, sorted, err := canonicalVRPSet(vrps)
	if err != nil {
		return err
	}
	root, err := rootFromSorted(sorted)
	if err != nil {
		return err
	}
	if root != end.RootDigest {
		return fmt.Errorf("%w: computed %s, end %s", ErrRootMismatch, root.Hex(), end.RootDigest.Hex())
	}

	now := r.now()
	if current := state.committed; current != nil {
		switch {
		case current.session == begin.Session:
			if begin.Serial == current.serial {
				if root != current.root {
					return ErrDuplicateSerial
				}
				updated := *current
				updated.updatedAt = now
				state.committed = &updated
				state.markStale = false
				return nil
			}
			if !IsForwardSerial(current.serial, begin.Serial) {
				return fmt.Errorf("%w: %d -> %d", ErrSerialRegression, current.serial, begin.Serial)
			}
		default:
			if _, retired := state.retired[begin.Session]; retired {
				return ErrSessionRetired
			}
		}
	}

	retired := state.retired
	if current := state.committed; current != nil && current.session != begin.Session {
		retired = cloneRetired(state.retired)
		retired[current.session] = struct{}{}
	}
	state.committed = &committedView{
		session:   begin.Session,
		serial:    begin.Serial,
		root:      root,
		set:       set,
		sorted:    sorted,
		updatedAt: now,
	}
	state.retired = retired
	state.markStale = false
	return nil
}

func (r *Router) completeDeltaLocked(state *routerCache, pending *pendingResponse) error {
	current := state.committed
	if current == nil {
		return ErrNeedReset
	}
	end, ok := pending.parts[pending.total-1]
	if !ok || end.Kind != MessageSerialEnd {
		return fmt.Errorf("%w: missing serial end", ErrIncompleteResponse)
	}
	if end.Session != current.session {
		return fmt.Errorf("%w: delta session %s, committed %s", ErrSessionMismatch, end.Session, current.session)
	}
	if end.FromSerial != current.serial || pending.from != current.serial {
		return fmt.Errorf("%w: delta starts at %d, committed %d", ErrSerialRegression, end.FromSerial, current.serial)
	}
	if end.Serial != end.ToSerial {
		return fmt.Errorf("%w: end serial %d differs from target %d", ErrMalformedMessage, end.Serial, end.ToSerial)
	}

	events := make([]SignedMessage, 0, pending.total-1)
	for index := uint32(0); index < pending.total-1; index++ {
		msg, exists := pending.parts[index]
		if !exists {
			return fmt.Errorf("%w: missing delta event %d", ErrIncompleteResponse, index)
		}
		if msg.Kind != MessageAdd && msg.Kind != MessageRemove {
			return fmt.Errorf("%w: event %d is %s", ErrMalformedMessage, index, msg.Kind)
		}
		if msg.Session != current.session || msg.FromSerial != pending.from || msg.ToSerial != pending.to {
			return fmt.Errorf("%w: event %d disagrees with response bounds", ErrMalformedMessage, index)
		}
		events = append(events, msg)
	}

	if end.ToSerial == current.serial {
		if len(events) != 0 || end.RootDigest != current.root {
			return ErrDuplicateSerial
		}
		updated := *current
		updated.updatedAt = r.now()
		state.committed = &updated
		state.markStale = false
		return nil
	}
	if !IsForwardSerial(current.serial, end.ToSerial) {
		return fmt.Errorf("%w: %d -> %d", ErrSerialRegression, current.serial, end.ToSerial)
	}
	if len(events) == 0 {
		return fmt.Errorf("%w: target serial %d has no events", ErrMissingEvent, end.ToSerial)
	}

	progress := current.serial
	for i, event := range events {
		switch {
		case event.Serial == progress:
			if i == 0 {
				return fmt.Errorf("%w: first event repeats committed serial %d", ErrSerialGap, progress)
			}
		case event.Serial == NextSerial(progress):
			progress = event.Serial
		default:
			return fmt.Errorf("%w: event serial %d after %d", ErrSerialGap, event.Serial, progress)
		}
	}
	if progress != end.ToSerial {
		return fmt.Errorf("%w: event coverage ends at %d, target %d", ErrMissingEvent, progress, end.ToSerial)
	}

	nextSet := cloneVRPMap(current.set)
	for _, event := range events {
		rec, err := canonicalVRP(event.VRP)
		if err != nil {
			return err
		}
		switch event.Kind {
		case MessageAdd:
			if _, exists := nextSet[rec.key]; exists {
				return fmt.Errorf("%w: %s", ErrAddConflict, rec.key)
			}
			nextSet[rec.key] = rec
		case MessageRemove:
			if _, exists := nextSet[rec.key]; !exists {
				return fmt.Errorf("%w: %s", ErrRemoveMissing, rec.key)
			}
			delete(nextSet, rec.key)
		}
	}
	nextSorted := sortedFromMap(nextSet)
	root, err := rootFromSorted(nextSorted)
	if err != nil {
		return err
	}
	if root != end.RootDigest {
		return fmt.Errorf("%w: computed %s, end %s", ErrRootMismatch, root.Hex(), end.RootDigest.Hex())
	}

	state.committed = &committedView{
		session:   current.session,
		serial:    end.ToSerial,
		root:      root,
		set:       nextSet,
		sorted:    nextSorted,
		updatedAt: r.now(),
	}
	state.markStale = false
	return nil
}

func cloneRetired(src map[SessionID]struct{}) map[SessionID]struct{} {
	dst := make(map[SessionID]struct{}, len(src)+1)
	for session := range src {
		dst[session] = struct{}{}
	}
	return dst
}

func (r *Router) lookupLocked(cacheID CacheID) (*routerCache, error) {
	state := r.stores[cacheID]
	if state == nil || len(state.key) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: %q", ErrUnknownCache, cacheID)
	}
	return state, nil
}

// View returns a coherent snapshot of a cache's committed state. The second
// result is false when no complete view has ever been committed.
func (r *Router) View(cacheID CacheID) (View, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state := r.stores[cacheID]
	if state == nil || state.committed == nil {
		return View{}, false
	}
	return r.makeViewLocked(state, r.now()), true
}

// CommittedView is an alias for View.
func (r *Router) CommittedView(cacheID CacheID) (View, bool) {
	return r.View(cacheID)
}

func (r *Router) makeViewLocked(state *routerCache, now time.Time) View {
	current := state.committed
	staleFor := now.Sub(current.updatedAt)
	if staleFor < 0 {
		staleFor = 0
	}
	status := r.viewStatusLocked(state, staleFor)
	return View{
		CacheID:    state.cacheID,
		Session:    current.session,
		Serial:     current.serial,
		RootDigest: current.root,
		VRPs:       current.copyVRPs(),
		UpdatedAt:  current.updatedAt,
		StaleFor:   staleFor,
		Status:     status,
	}
}

func (r *Router) viewStatusLocked(state *routerCache, staleFor time.Duration) ViewStatus {
	if state.committed == nil {
		return ViewUnknown
	}
	if staleFor >= r.maxStale {
		return ViewExpired
	}
	if state.markStale || staleFor >= r.staleAfter {
		return ViewStale
	}
	return ViewFresh
}

// Validate checks one route prefix and origin against a cache view. The
// returned status is never Valid when the view is stale or expired.
func (r *Router) Validate(cacheID CacheID, prefix netip.Prefix, originAS uint32) Validation {
	r.mu.RLock()
	defer r.mu.RUnlock()

	state := r.stores[cacheID]
	if state == nil || state.committed == nil {
		return Validation{Status: StatusUnknown}
	}
	current := state.committed
	now := r.now()
	staleFor := now.Sub(current.updatedAt)
	if staleFor < 0 {
		staleFor = 0
	}
	status := r.viewStatusLocked(state, staleFor)
	view := r.makeViewLocked(state, now)

	switch status {
	case ViewExpired:
		if r.policy == PolicyUnknown {
			return Validation{Status: StatusUnknown, View: view}
		}
		return Validation{Status: StatusInvalid, View: view}
	case ViewStale:
		var matches []VRP
		for _, rec := range current.sorted {
			if vrpMatches(rec, prefix, originAS) {
				matches = append(matches, rec.vrp)
			}
		}
		return Validation{Status: StatusStale, Matches: matches, View: view}
	case ViewFresh:
		var matches []VRP
		for _, rec := range current.sorted {
			if vrpMatches(rec, prefix, originAS) {
				matches = append(matches, rec.vrp)
			}
		}
		if len(matches) == 0 {
			return Validation{Status: StatusInvalid, View: view}
		}
		return Validation{Status: StatusValid, Matches: matches, View: view}
	default:
		return Validation{Status: StatusUnknown, View: view}
	}
}

// ValidatePrefix is the string-prefix form of Validate.
func (r *Router) ValidatePrefix(cacheID CacheID, prefix string, originAS uint32) (Validation, error) {
	parsed, err := netip.ParsePrefix(prefix)
	if err != nil {
		return Validation{}, fmt.Errorf("%w: %v", ErrMalformedMessage, err)
	}
	return r.Validate(cacheID, parsed, originAS), nil
}

// SetPreference replaces the preferred-cache order. Unknown cache IDs are
// rejected. A cache need not yet be synchronised; selection simply skips it
// until it has a complete committed view.
func (r *Router) SetPreference(cacheIDs []CacheID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, cacheID := range cacheIDs {
		if _, exists := r.stores[cacheID]; !exists {
			return fmt.Errorf("%w: %q", ErrUnknownCache, cacheID)
		}
	}
	r.preference = append([]CacheID(nil), cacheIDs...)
	seen := make(map[CacheID]struct{}, len(r.preference))
	deduplicated := r.preference[:0]
	for _, cacheID := range r.preference {
		if _, exists := seen[cacheID]; exists {
			continue
		}
		seen[cacheID] = struct{}{}
		deduplicated = append(deduplicated, cacheID)
	}
	r.preference = deduplicated
	return nil
}

// PromoteCache changes the first preferred cache. It only accepts a cache
// with a complete committed view; staged or partial state is never eligible.
func (r *Router) PromoteCache(cacheID CacheID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.stores[cacheID]
	if state == nil {
		return fmt.Errorf("%w: %q", ErrUnknownCache, cacheID)
	}
	if state.committed == nil {
		return ErrNotCommitted
	}
	ordered := []CacheID{cacheID}
	for _, existing := range r.preference {
		if existing != cacheID {
			ordered = append(ordered, existing)
		}
	}
	r.preference = ordered
	return nil
}

// SetPreferredCache is an alias for PromoteCache.
func (r *Router) SetPreferredCache(cacheID CacheID) error {
	return r.PromoteCache(cacheID)
}

// SwitchPreferred is an alias for PromoteCache.
func (r *Router) SwitchPreferred(cacheID CacheID) error {
	return r.PromoteCache(cacheID)
}

// PreferredCache returns the first preferred cache with a complete committed
// view.
func (r *Router) PreferredCache() (CacheID, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, cacheID := range r.effectivePreferenceLocked() {
		if state := r.stores[cacheID]; state != nil && state.committed != nil {
			return cacheID, true
		}
	}
	return "", false
}

// PreferredView returns only a complete committed view. A cache undergoing a
// reset is skipped until its new view has passed root validation.
func (r *Router) PreferredView() (View, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, cacheID := range r.effectivePreferenceLocked() {
		state := r.stores[cacheID]
		if state == nil || state.committed == nil {
			continue
		}
		return r.makeViewLocked(state, r.now()), true
	}
	return View{}, false
}

func (r *Router) effectivePreferenceLocked() []CacheID {
	if len(r.preference) != 0 {
		return r.preference
	}
	ids := make([]CacheID, 0, len(r.stores))
	for cacheID := range r.stores {
		ids = append(ids, cacheID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// RetiredSessions returns a copy of sessions that may never be committed
// again for this cache. It is useful for diagnostics and persistence.
func (r *Router) RetiredSessions(cacheID CacheID) []SessionID {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state := r.stores[cacheID]
	if state == nil {
		return nil
	}
	out := make([]SessionID, 0, len(state.retired))
	for session := range state.retired {
		out = append(out, session)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].String() < out[j].String()
	})
	return out
}
