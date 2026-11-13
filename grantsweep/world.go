package grantsweep

import (
	"fmt"
	"time"
)

const (
	tokenTTL    = time.Hour
	pendingWin  = time.Minute
	baseTime    = int64(1_700_000_000)
	pendingKind = "pending"
	activeKind  = "active"
)

// World is one concrete execution state under a Policy.
type World struct {
	policy  Policy
	now     time.Time
	grants  map[string]*record
	request map[string]string // requestID -> grantID (reference binds creator)

	// inFlightRetire models a non-atomic retire: under NonAtomicRetire the
	// status flip and generation bump are separate schedulable steps.
	inFlightRetire *retireJob
}

type retireJob struct {
	grantID string
	caller  Principal
	step    int
}

// NewWorld creates an empty world at a fixed base time.
func NewWorld(p Policy) *World {
	return &World{
		policy:  p,
		now:     time.Unix(baseTime, 0),
		grants:  make(map[string]*record),
		request: make(map[string]string),
	}
}

// Clone deep-copies the world so BFS can branch without aliasing.
func (w *World) Clone() *World {
	cp := &World{
		policy:  w.policy,
		now:     w.now,
		grants:  make(map[string]*record, len(w.grants)),
		request: make(map[string]string, len(w.request)),
	}
	for k, r := range w.grants {
		cp.grants[k] = r.clone()
	}
	for k, v := range w.request {
		cp.request[k] = v
	}
	if w.inFlightRetire != nil {
		job := *w.inFlightRetire
		cp.inFlightRetire = &job
	}
	return cp
}

// CloneWithPolicy returns a deep copy running under a different policy.
func (w *World) CloneWithPolicy(p Policy) *World {
	cp := w.Clone()
	cp.policy = p
	return cp
}

// Advance moves the host clock.
func (w *World) Advance(d time.Duration) { w.now = w.now.Add(d) }

// CreateSpec describes a BeginCreate action.
type CreateSpec struct {
	GrantID           string
	RequestID         string
	Creator           Principal
	KeyID             string
	KeyVersion        uint64
	GranteePrincipal  Principal
	RetiringPrincipal Principal
	Operations        []Operation
	Constraints       Context
	Subset            Context
	AllowPending      bool
}

// BeginCreate persists a Pending grant (modeling host Grant permission as
// granted) and returns the Pending token. Replaying a RequestID returns the
// existing record rather than creating a duplicate.
func (w *World) BeginCreate(spec CreateSpec) (*Token, error) {
	key := string(spec.Creator) + ":" + spec.RequestID
	if id, ok := w.request[key]; ok {
		rec := w.grants[id]
		if t := rec.latestToken(); t != nil {
			return t, nil
		}
		return nil, fmt.Errorf("no token for replay of %q", spec.RequestID)
	}
	if _, clash := w.grants[spec.GrantID]; clash {
		return nil, fmt.Errorf("GrantID %q already exists", spec.GrantID)
	}
	g := &Grant{
		GrantID:           spec.GrantID,
		KeyID:             spec.KeyID,
		KeyVersion:        spec.KeyVersion,
		GranteePrincipal:  spec.GranteePrincipal,
		RetiringPrincipal: spec.RetiringPrincipal,
		Operations:        append([]Operation(nil), spec.Operations...),
		Constraints:       cloneContext(spec.Constraints),
		Subset:            cloneContext(spec.Subset),
		Status:            StatusPending,
	}
	tok := &Token{
		GrantID: spec.GrantID, Kind: pendingKind,
		Expiry: w.now.Add(tokenTTL), Owner: spec.Creator,
	}
	rec := &record{
		grant:  g,
		tokens: []*Token{tok},
		pending: &pendingInfo{
			requestID: spec.RequestID,
			creator:   spec.Creator,
			allowed:   spec.AllowPending,
			deadline:  w.now.Add(pendingWin),
		},
	}
	w.grants[spec.GrantID] = rec
	w.request[key] = spec.GrantID
	return tok, nil
}

// Activate flips a Pending grant to Active and mints an Active token.
func (w *World) Activate(creator Principal, requestID string) (*Token, error) {
	rec, err := w.lookupRequest(creator, requestID)
	if err != nil {
		return nil, err
	}
	if rec.grant.Status == StatusActive {
		return rec.latestToken(), nil
	}
	if rec.grant.Status == StatusRetired {
		return nil, fmt.Errorf("grant retired before activation")
	}
	rec.grant.Status = StatusActive
	tok := &Token{
		GrantID: rec.grant.GrantID, Generation: rec.grant.Generation,
		Kind: activeKind, Expiry: w.now.Add(tokenTTL), Owner: rec.grant.GranteePrincipal,
	}
	rec.tokens = append(rec.tokens, tok)
	rec.pending = nil
	return tok, nil
}

func (w *World) lookupRequest(creator Principal, requestID string) (*record, error) {
	id, ok := w.request[string(creator)+":"+requestID]
	if !ok {
		return nil, fmt.Errorf("unknown Create request %q for %q", requestID, creator)
	}
	return w.grants[id], nil
}

func (r *record) latestToken() *Token {
	if len(r.tokens) == 0 {
		return nil
	}
	return r.tokens[len(r.tokens)-1]
}

// UseSpec describes a token-protected KMS call.
type UseSpec struct {
	Caller       Principal
	Operation    Operation
	KeyID        string
	KeyVersion   uint64
	Context      Context
	Token        *Token
	RequestID    string
	AllowPending bool
}

// Use evaluates one call and reports whether the model allows it.
func (w *World) Use(spec UseSpec) bool {
	return w.evaluate(spec, nil)
}

// UseSnapshot captures the grant state a concurrent Use would observe at
// schedule time. Under NonAtomicRetire the matching DecideUse consults this
// snapshot instead of re-reading live state, reproducing the TOCTOU window.
type UseSnapshot struct {
	spec       UseSpec
	generation uint64
	status     Status
	record     *record
}

// SnapshotUse takes the read-phase snapshot of a call.
func (w *World) SnapshotUse(spec UseSpec) *UseSnapshot {
	if spec.Token == nil {
		return &UseSnapshot{spec: spec}
	}
	rec, ok := w.grants[spec.Token.GrantID]
	if !ok {
		return &UseSnapshot{spec: spec}
	}
	return &UseSnapshot{
		spec:       spec,
		generation: rec.grant.Generation,
		status:     rec.grant.Status,
		record:     rec,
	}
}

// DecideUse finishes a two-phase call. Non-atomic models decide against the
// captured snapshot; the reference always re-reads the current record.
func (w *World) DecideUse(snap *UseSnapshot) bool {
	if w.policy.NonAtomicRetire {
		return w.evaluate(snap.spec, snap)
	}
	return w.Use(snap.spec)
}

func (w *World) evaluate(spec UseSpec, snap *UseSnapshot) bool {
	if spec.Token == nil {
		return false
	}
	if !w.now.Before(spec.Token.Expiry) {
		return false
	}
	rec, ok := w.grants[spec.Token.GrantID]
	if !ok {
		return false
	}
	g := rec.grant

	gen := g.Generation
	status := g.Status
	if snap != nil && snap.record != nil {
		gen = snap.generation
		status = snap.status
	}

	// Reference binding: token generation must match the current generation.
	// GrantIDLookup ignores this and trusts any token found by GrantID.
	if !w.policy.GrantIDLookup && spec.Token.Generation != gen {
		return false
	}

	switch status {
	case StatusRetired:
		// GrantID-only lookup models a stale cache entry that ignores the
		// record's current status, allowing an old token to bypass retirement.
		if w.policy.GrantIDLookup {
			break
		}
		return false
	case StatusPending:
		if !w.allowPending(rec, spec) {
			return false
		}
	case StatusActive:
		if !w.policy.IgnoreCaller && spec.Caller != g.GranteePrincipal {
			return false
		}
	}

	if !w.policy.IgnoreOperation && !containsOp(g.Operations, spec.Operation) {
		return false
	}
	if spec.KeyID != g.KeyID {
		return false
	}
	if !w.policy.IgnoreKeyVersion && spec.KeyVersion != g.KeyVersion {
		return false
	}
	return w.contextOK(g, spec.Context)
}

func (w *World) allowPending(rec *record, spec UseSpec) bool {
	info := rec.pending
	if info == nil || !spec.AllowPending || !info.allowed {
		return false
	}
	if !w.now.Before(info.deadline) {
		return false
	}
	if w.policy.CrossClientPending {
		return spec.RequestID == info.requestID
	}
	return spec.Caller == info.creator && spec.RequestID == info.requestID
}

func (w *World) contextOK(g *Grant, call Context) bool {
	if g.Constraints != nil {
		if !w.policy.IgnoreExtraContext && len(call) != len(g.Constraints) {
			return false
		}
		for k, v := range g.Constraints {
			if call[k] != v {
				return false
			}
		}
	}
	if g.Subset != nil && !w.policy.IgnoreSubset {
		for k, v := range g.Subset {
			if got, ok := call[k]; !ok || got != v {
				return false
			}
		}
	}
	return true
}

// Retire retires a grant. Under NonAtomicRetire the transition is split into
// BeginRetire/FinishRetire so the scheduler can interleave a Use between the
// status flip and the generation increment.
func (w *World) Retire(caller Principal, grantID string) error {
	if w.policy.NonAtomicRetire {
		if err := w.BeginRetire(caller, grantID); err != nil {
			return err
		}
		return w.FinishRetire()
	}
	return w.atomicRetire(caller, grantID)
}

func (w *World) atomicRetire(caller Principal, grantID string) error {
	rec, ok := w.grants[grantID]
	if !ok {
		return fmt.Errorf("unknown grant %q", grantID)
	}
	g := rec.grant
	if caller != g.RetiringPrincipal && caller != g.GranteePrincipal {
		return fmt.Errorf("caller %q may not retire", caller)
	}
	if g.Status == StatusRetired {
		return nil
	}
	g.Status = StatusRetired
	g.Generation++
	rec.pending = nil
	return nil
}

// BeginRetire starts a non-atomic retire: access control and status flip only.
func (w *World) BeginRetire(caller Principal, grantID string) error {
	rec, ok := w.grants[grantID]
	if !ok {
		return fmt.Errorf("unknown grant %q", grantID)
	}
	g := rec.grant
	if caller != g.RetiringPrincipal && caller != g.GranteePrincipal {
		return fmt.Errorf("caller %q may not retire", caller)
	}
	if g.Status == StatusRetired {
		return nil
	}
	g.Status = StatusRetired
	w.inFlightRetire = &retireJob{grantID: grantID, caller: caller, step: 1}
	return nil
}

// FinishRetire performs the deferred generation increment.
func (w *World) FinishRetire() error {
	job := w.inFlightRetire
	if job == nil {
		return fmt.Errorf("no retire in flight")
	}
	rec := w.grants[job.grantID]
	rec.grant.Generation++
	rec.pending = nil
	w.inFlightRetire = nil
	return nil
}

// HasInFlightRetire reports whether a generation bump is pending.
func (w *World) HasInFlightRetire() bool { return w.inFlightRetire != nil }

func containsOp(ops []Operation, op Operation) bool {
	for _, candidate := range ops {
		if candidate == op {
			return true
		}
	}
	return false
}
