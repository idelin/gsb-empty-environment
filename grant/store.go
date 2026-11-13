package grant

import (
	"crypto/sha256"
	"errors"
	"sync"
	"time"
)

// Clock supplies the current time. Production hosts pass a Clock backed by
// time.Now; tests use a controllable fake. Pending propagation windows are
// always judged by the host clock, never by a client-supplied timestamp.
type Clock interface {
	Now() time.Time
}

// SystemClock is the default Clock backed by time.Now.
type SystemClock struct{}

// Now returns the current wall-clock time with a monotonic reading.
func (SystemClock) Now() time.Time { return time.Now() }

// KeyResource identifies one versioned KMS key.
type KeyResource struct {
	KeyID   string
	Version uint64
}

// Authority is the host permission callback. It runs while no store lock is
// held, so implementations must not call back into the same Store.
type Authority interface {
	CanGrant(caller Principal, key KeyResource, constraints, subset Context) error
}

// AuthorityFunc adapts a function to Authority.
type AuthorityFunc func(caller Principal, key KeyResource, constraints, subset Context) error

// CanGrant calls f.
func (f AuthorityFunc) CanGrant(caller Principal, key KeyResource, constraints, subset Context) error {
	return f(caller, key, constraints, subset)
}

// CreateRequest is a grant creation request. RequestID is the idempotency key
// and is scoped to the Creator principal.
type CreateRequest struct {
	GrantID           string
	RequestID         string
	Creator           Principal
	Key               KeyResource
	GranteePrincipal  Principal
	RetiringPrincipal Principal
	Operations        []Operation
	Constraints       Context
	Subset            Context

	// TTL is the lifetime of every minted token.
	TTL time.Duration
	// AllowPending permits Pending-state tokens to be used during the
	// propagation window. PendingWindow is measured on the host clock.
	AllowPending  bool
	PendingWindow time.Duration
}

func (r CreateRequest) normalized() CreateRequest {
	r.Operations = append([]Operation(nil), r.Operations...)
	r.Constraints = r.Constraints.Clone()
	r.Subset = r.Subset.Clone()
	return r
}

// AuthorizeRequest is a token-protected KMS call.
type AuthorizeRequest struct {
	Caller    Principal
	Operation Operation
	Key       KeyResource
	Context   Context
	Token     string

	// RequestID is only consulted for Pending tokens and must match the
	// original Create request.
	RequestID string
	// AllowPending is the caller's explicit opt-in to Pending propagation.
	AllowPending bool
}

type pendingInfo struct {
	requestID string
	creator   Principal
	deadline  time.Time
}

type storedGrant struct {
	grant        *Grant
	pending      *pendingInfo
	pendingToken *Token
	activeToken  *Token
	failed       error
	createReq    CreateRequest
}

type requestKey struct {
	creator   Principal
	requestID string
}

// Store is the linearizable grant state machine. All state transitions happen
// under a single mutex, so Create/Authorize/Retire are serialized and Retire
// is atomic with respect to every authorization.
type Store struct {
	serviceKey []byte
	clock      Clock
	authority  Authority

	mu       sync.Mutex
	grants   map[string]*storedGrant
	requests map[requestKey]string
}

// NewStore builds a Store. serviceKey must be at least 32 random bytes and is
// the HMAC key for every grant token.
func NewStore(serviceKey []byte, clock Clock, authority Authority) (*Store, error) {
	if len(serviceKey) < sha256.Size {
		return nil, errors.Join(ErrInvalidRequest, errors.New("grant: service key must be at least 32 bytes"))
	}
	if clock == nil {
		clock = SystemClock{}
	}
	if authority == nil {
		return nil, errors.Join(ErrInvalidRequest, errors.New("grant: authority callback is required"))
	}
	key := append([]byte(nil), serviceKey...)
	return &Store{
		serviceKey: key,
		clock:      clock,
		authority:  authority,
		grants:     make(map[string]*storedGrant),
		requests:   make(map[requestKey]string),
	}, nil
}

func validateCreate(req CreateRequest) error {
	switch {
	case req.GrantID == "":
		return errors.Join(ErrInvalidRequest, errors.New("grant: missing GrantID"))
	case req.RequestID == "":
		return errors.Join(ErrInvalidRequest, errors.New("grant: missing RequestID"))
	case req.Creator == "":
		return errors.Join(ErrInvalidRequest, errors.New("grant: missing Creator"))
	case req.GranteePrincipal == "":
		return errors.Join(ErrInvalidRequest, errors.New("grant: missing GranteePrincipal"))
	case req.Key.KeyID == "":
		return errors.Join(ErrInvalidRequest, errors.New("grant: missing KeyID"))
	case len(req.Operations) == 0:
		return errors.Join(ErrInvalidRequest, errors.New("grant: at least one operation required"))
	case req.TTL <= 0:
		return errors.Join(ErrInvalidRequest, errors.New("grant: token TTL must be positive"))
	case req.AllowPending && req.PendingWindow <= 0:
		return errors.Join(ErrInvalidRequest, errors.New("grant: pending window must be positive"))
	}
	for _, op := range req.Operations {
		if op == "" {
			return errors.Join(ErrInvalidRequest, errors.New("grant: empty operation"))
		}
	}
	return nil
}

// BeginCreate persists a Pending grant and mints its Pending token, then asks
// the host Authority for Grant permission. It never activates the grant.
// Replaying the same (Creator, RequestID) returns the original record, so a
// lost response never creates a duplicate grant.
func (s *Store) BeginCreate(req CreateRequest) (*Grant, *Token, error) {
	if err := validateCreate(req); err != nil {
		return nil, nil, err
	}
	req = req.normalized()
	now := s.clock.Now()

	rk := requestKey{creator: req.Creator, requestID: req.RequestID}
	s.mu.Lock()
	if existingID, ok := s.requests[rk]; ok {
		existing := s.grants[existingID]
		s.mu.Unlock()
		return s.replayCreate(existing, req)
	}
	if _, clash := s.grants[req.GrantID]; clash {
		s.mu.Unlock()
		return nil, nil, errors.Join(ErrConflict, errors.New("grant: GrantID already exists"))
	}

	g := &Grant{
		GrantID:           req.GrantID,
		KeyID:             req.Key.KeyID,
		KeyVersion:        req.Key.Version,
		GranteePrincipal:  req.GranteePrincipal,
		RetiringPrincipal: req.RetiringPrincipal,
		Operations:        append([]Operation(nil), req.Operations...),
		Constraints:       req.Constraints.Clone(),
		Subset:            req.Subset.Clone(),
		Generation:        0,
		Status:            StatusPending,
	}
	token, err := MintToken(s.serviceKey, g, req.TTL, now)
	if err != nil {
		s.mu.Unlock()
		return nil, nil, err
	}
	stored := &storedGrant{
		grant:        g,
		pendingToken: token,
		createReq:    req,
		pending: &pendingInfo{
			requestID: req.RequestID,
			creator:   req.Creator,
		},
	}
	if req.AllowPending {
		stored.pending.deadline = now.Add(req.PendingWindow)
	}
	s.grants[g.GrantID] = stored
	s.requests[rk] = g.GrantID
	s.mu.Unlock()

	// Permission is confirmed after the Pending record is durable, but while
	// the grant is still unusable except through the gated pending path.
	if err := s.authority.CanGrant(req.Creator, req.Key, req.Constraints, req.Subset); err != nil {
		denied := errors.Join(ErrPermission, err)
		s.mu.Lock()
		stored.failed = denied
		s.mu.Unlock()
		return nil, nil, denied
	}
	return g.Clone(), cloneToken(token), nil
}

func (s *Store) replayCreate(stored *storedGrant, req CreateRequest) (*Grant, *Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if stored.failed != nil {
		return nil, nil, stored.failed
	}
	if !sameCreateRequest(stored.createReq, req) {
		return nil, nil, errors.Join(ErrConflict, errors.New("grant: RequestID bound to a different request"))
	}
	token := stored.activeToken
	if token == nil {
		token = stored.pendingToken
	}
	return stored.grant.Clone(), cloneToken(token), nil
}

func sameCreateRequest(a, b CreateRequest) bool {
	if a.GrantID != b.GrantID || a.Creator != b.Creator || a.Key != b.Key ||
		a.GranteePrincipal != b.GranteePrincipal || a.RetiringPrincipal != b.RetiringPrincipal ||
		len(a.Operations) != len(b.Operations) {
		return false
	}
	opSet := make(map[Operation]bool, len(a.Operations))
	for _, op := range a.Operations {
		opSet[op] = true
	}
	for _, op := range b.Operations {
		if !opSet[op] {
			return false
		}
	}
	return sameContext(a.Constraints, b.Constraints) && sameContext(a.Subset, b.Subset)
}

func sameContext(a, b Context) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// Activate moves a Pending grant to Active and mints the Active token. Only
// the original Creator can activate; replaying activation after a lost
// response returns the same Active token.
func (s *Store) Activate(creator Principal, requestID string) (*Grant, *Token, error) {
	rk := requestKey{creator: creator, requestID: requestID}
	s.mu.Lock()
	defer s.mu.Unlock()

	grantID, ok := s.requests[rk]
	if !ok {
		return nil, nil, errors.Join(ErrNotFound, errors.New("grant: unknown Create request"))
	}
	stored := s.grants[grantID]
	if stored.failed != nil {
		return nil, nil, stored.failed
	}
	if stored.grant.Status == StatusActive {
		return stored.grant.Clone(), cloneToken(stored.activeToken), nil
	}
	if stored.grant.Status == StatusRetired {
		return nil, nil, deny(ErrRetired, "grant: grant %s retired before activation", grantID)
	}

	stored.grant.Status = StatusActive
	token, err := MintToken(s.serviceKey, stored.grant, stored.createReq.TTL, s.clock.Now())
	if err != nil {
		return nil, nil, err
	}
	stored.activeToken = token
	stored.pending = nil
	return stored.grant.Clone(), cloneToken(token), nil
}

// Create is the convenience path: persist Pending, confirm Grant permission
// and activate in one call.
func (s *Store) Create(req CreateRequest) (*Grant, *Token, error) {
	if _, _, err := s.BeginCreate(req); err != nil {
		return nil, nil, err
	}
	return s.Activate(req.Creator, req.RequestID)
}

// Get returns a snapshot of a grant.
func (s *Store) Get(grantID string) (*Grant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, ok := s.grants[grantID]
	if !ok {
		return nil, errors.Join(ErrNotFound, errors.New("grant: unknown GrantID"))
	}
	return stored.grant.Clone(), nil
}

// Authorize validates a token-protected call. The token is verified
// cryptographically before any grant lookup; the lookup then requires the same
// generation and an identical full digest, followed by principal, operation,
// key/version and context checks.
func (s *Store) Authorize(req AuthorizeRequest) (*Grant, error) {
	// 1. Token first: malformed or forged tokens never reach state lookup.
	token, err := ParseToken(s.serviceKey, req.Token, s.clock.Now())
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 2. Read the same-generation grant and compare the full digest.
	stored, ok := s.grants[token.GrantID]
	if !ok {
		return nil, deny(ErrNotFound, "grant: unknown GrantID %q", token.GrantID)
	}
	if stored.failed != nil {
		return nil, deny(ErrPermission, "grant: grant creation was rejected")
	}
	g := stored.grant
	if g.Generation != token.Generation {
		return nil, deny(ErrDenied, "grant: token generation %d != grant generation %d", token.Generation, g.Generation)
	}
	if g.Digest() != token.Digest {
		return nil, deny(ErrDenied, "grant: token does not match current grant record")
	}

	// 3. Lifecycle gate.
	switch g.Status {
	case StatusRetired:
		return nil, deny(ErrRetired, "grant: grant %s is retired", g.GrantID)
	case StatusPending:
		if err := s.authorizePending(stored, req); err != nil {
			return nil, err
		}
	case StatusActive:
		if req.Caller != g.GranteePrincipal {
			return nil, deny(ErrDenied, "grant: caller %q is not grantee %q", req.Caller, g.GranteePrincipal)
		}
	default:
		return nil, deny(ErrDenied, "grant: grant in unknown state")
	}

	// 4. Operation, resource and context checks.
	if !containsOp(g.Operations, req.Operation) {
		return nil, deny(ErrDenied, "grant: operation %q not permitted", req.Operation)
	}
	if req.Key.KeyID != g.KeyID || req.Key.Version != g.KeyVersion {
		return nil, deny(ErrDenied, "grant: key %s/v%d does not match grant %s/v%d",
			req.Key.KeyID, req.Key.Version, g.KeyID, g.KeyVersion)
	}
	if err := checkContext(g, req.Context); err != nil {
		return nil, err
	}
	return g.Clone(), nil
}

// authorizePending enforces the Pending propagation rules: explicit caller
// opt-in, host-clock window, and binding to the exact Create requestID and
// creator. Another client cannot use a leaked Pending token.
func (s *Store) authorizePending(stored *storedGrant, req AuthorizeRequest) error {
	info := stored.pending
	if info == nil {
		return deny(ErrPendingWindow, "grant: pending metadata missing")
	}
	if !req.AllowPending {
		return deny(ErrPendingWindow, "grant: caller did not opt into Pending propagation")
	}
	now := s.clock.Now()
	if info.deadline.IsZero() || !now.Before(info.deadline) {
		return deny(ErrPendingWindow, "grant: pending propagation window is closed")
	}
	if req.Caller != info.creator {
		return deny(ErrDenied, "grant: pending token may only be used by creator %q", info.creator)
	}
	if req.RequestID != info.requestID {
		return deny(ErrDenied, "grant: pending token requestID does not match Create request")
	}
	return nil
}

// Retire retires a grant. Only the retiring principal or the grantee may do
// so. The generation is incremented atomically with the status change, so the
// digest changes and every previously minted token fails verification against
// the current record.
func (s *Store) Retire(caller Principal, grantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored, ok := s.grants[grantID]
	if !ok {
		return errors.Join(ErrNotFound, errors.New("grant: unknown GrantID"))
	}
	g := stored.grant
	if caller != g.RetiringPrincipal && caller != g.GranteePrincipal {
		return deny(ErrDenied, "grant: caller %q may not retire grant", caller)
	}
	if g.Status == StatusRetired {
		return nil
	}
	g.Status = StatusRetired
	g.Generation++
	stored.pending = nil
	return nil
}

func containsOp(ops []Operation, op Operation) bool {
	for _, candidate := range ops {
		if candidate == op {
			return true
		}
	}
	return false
}

// checkContext applies both constraint kinds. Equality constraints require an
// exact context match (any extra or missing key is rejected); subset
// constraints require every listed pair to be present while extra keys remain
// allowed.
func checkContext(g *Grant, call Context) error {
	if g.Constraints != nil {
		if len(call) != len(g.Constraints) {
			return deny(ErrDenied, "grant: equality context has %d pairs, grant requires %d", len(call), len(g.Constraints))
		}
		for k, v := range g.Constraints {
			if call[k] != v {
				return deny(ErrDenied, "grant: equality context mismatch on key %q", k)
			}
		}
	}
	if g.Subset != nil {
		for k, v := range g.Subset {
			got, ok := call[k]
			if !ok || got != v {
				return deny(ErrDenied, "grant: subset context missing required key %q", k)
			}
		}
	}
	return nil
}

func cloneToken(t *Token) *Token {
	if t == nil {
		return nil
	}
	cp := *t
	cp.TokenID = append([]byte(nil), t.TokenID...)
	return &cp
}
