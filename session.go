package netsec

import (
	"context"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"
)

// SessionState is the externally visible transaction state of a policy
// session. A session is intentionally single-use once policy commitments have
// been installed in the TPM.
type SessionState uint8

const (
	StateIdle SessionState = iota
	StateAuthorizing
	StateAuthorized
	StateSubmitting
	StateUnknown
	StateConsumed
	StateExpired
	StateSaved
	StateFlushed
	StateInvalid
)

func (s SessionState) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateAuthorizing:
		return "authorizing"
	case StateAuthorized:
		return "authorized"
	case StateSubmitting:
		return "submitting"
	case StateUnknown:
		return "unknown"
	case StateConsumed:
		return "consumed"
	case StateExpired:
		return "expired"
	case StateSaved:
		return "saved"
	case StateFlushed:
		return "flushed"
	case StateInvalid:
		return "invalid"
	default:
		return fmt.Sprintf("state(%d)", uint8(s))
	}
}

// Binding is the identity of one policy-session generation. All fields are
// copied into the session and rechecked before authorization and submission.
type Binding struct {
	StartupCount  uint64
	SessionHandle Handle
	Generation    uint64
	HashAlg       Algorithm
	NonceTPM      []byte
	PolicyDigest  []byte
	Principal     string
}

func (b Binding) clone() Binding {
	b.NonceTPM = append([]byte(nil), b.NonceTPM...)
	b.PolicyDigest = append([]byte(nil), b.PolicyDigest...)
	return b
}

// SessionOptions configures a new or restored policy session.
type SessionOptions struct {
	Handle         Handle
	HashAlg        Algorithm
	NonceTPM       []byte
	PolicyDigest   []byte
	Principal      string
	StartupCount   uint64
	VerifyResponse func(context.Context, ResponseCheck) error
	VerifyRecovery func(context.Context, RecoveryProof) error

	// AllowUnregisteredContext permits ContextLoad for a blob that this
	// Authority did not save. This disables host-side commitment history for
	// that blob and should only be used with an independently authenticated
	// context record.
	AllowUnregisteredContext bool
}

// CPHashCommitment is the immutable approval object installed by
// TPM2_PolicyCpHash.
type CPHashCommitment struct {
	CommandCode        CommandCode
	Handles            []Handle
	Names              []Name
	Parameters         []byte
	CPHash             []byte
	PolicyDigestBefore []byte
	PolicyDigestAfter  []byte
}

// NameHashCommitment records the exact canonical Name set installed by
// TPM2_PolicyNameHash.
type NameHashCommitment struct {
	Names              []Name
	NameHash           []byte
	PolicyDigestBefore []byte
	PolicyDigestAfter  []byte
}

// ORCommitment records both the complete branch set and the one selected
// branch. A restored session generation never inherits this record.
type ORCommitment struct {
	Branches           [][]byte
	Selected           int
	SelectedDigest     []byte
	PolicyDigestBefore []byte
	PolicyDigestAfter  []byte
}

// Commitments is an audit snapshot of what the current session generation has
// committed. All slices are copied by Commitments below.
type Commitments struct {
	OR       *ORCommitment
	NameHash *NameHashCommitment
	CPHash   *CPHashCommitment
}

func (c Commitments) clone() Commitments {
	var out Commitments
	if c.OR != nil {
		or := *c.OR
		or.Branches = make([][]byte, len(c.OR.Branches))
		for i := range c.OR.Branches {
			or.Branches[i] = append([]byte(nil), c.OR.Branches[i]...)
		}
		or.SelectedDigest = append([]byte(nil), c.OR.SelectedDigest...)
		or.PolicyDigestBefore = append([]byte(nil), c.OR.PolicyDigestBefore...)
		or.PolicyDigestAfter = append([]byte(nil), c.OR.PolicyDigestAfter...)
		out.OR = &or
	}
	if c.NameHash != nil {
		nh := *c.NameHash
		nh.Names = cloneNames(c.NameHash.Names)
		nh.NameHash = append([]byte(nil), c.NameHash.NameHash...)
		nh.PolicyDigestBefore = append([]byte(nil), c.NameHash.PolicyDigestBefore...)
		nh.PolicyDigestAfter = append([]byte(nil), c.NameHash.PolicyDigestAfter...)
		out.NameHash = &nh
	}
	if c.CPHash != nil {
		cp := *c.CPHash
		cp.Handles = append([]Handle(nil), c.CPHash.Handles...)
		cp.Names = cloneNames(c.CPHash.Names)
		cp.Parameters = append([]byte(nil), c.CPHash.Parameters...)
		cp.CPHash = append([]byte(nil), c.CPHash.CPHash...)
		cp.PolicyDigestBefore = append([]byte(nil), c.CPHash.PolicyDigestBefore...)
		cp.PolicyDigestAfter = append([]byte(nil), c.CPHash.PolicyDigestAfter...)
		out.CPHash = &cp
	}
	return out
}

func cloneNames(names []Name) []Name {
	out := make([]Name, len(names))
	for i := range names {
		out[i] = append(Name(nil), names[i]...)
	}
	return out
}

// UnknownState describes a command whose dispatch or response could not be
// proven either way. The session cannot be reused or replayed until
// Reconcile verifies quote and audit evidence.
type UnknownState struct {
	SessionHandle Handle
	Generation    uint64
	CPHash        []byte
	Cause         string
	At            time.Time
	PolicyChange  bool
}

func (u *UnknownState) clone() *UnknownState {
	if u == nil {
		return nil
	}
	out := *u
	out.CPHash = append([]byte(nil), u.CPHash...)
	return &out
}

// ORSelection selects one branch that must equal the current policy digest.
type ORSelection struct {
	Branches [][]byte
	Selected int
}

func (o *ORSelection) clone() *ORSelection {
	if o == nil {
		return nil
	}
	out := &ORSelection{Selected: o.Selected, Branches: make([][]byte, len(o.Branches))}
	for i := range o.Branches {
		out.Branches[i] = append([]byte(nil), o.Branches[i]...)
	}
	return out
}

// ResponseCheck is passed to an optional response verifier. It is invoked
// while the session is exclusively reserved and before the transaction is
// marked complete.
type ResponseCheck struct {
	CPHash       []byte
	PolicyDigest []byte
	NonceTPM     []byte
	Auth         AuthValues
	Command      CommandTemplate
	Response     Response
}

// RecoveryOutcome is the audited result of an unknown command.
type RecoveryOutcome uint8

const (
	RecoveryNotApplied RecoveryOutcome = iota
	RecoveryApplied
)

// RecoveryProof must be produced by a fresh Quote and TPM audit inspection.
// The verifier is application-supplied because signature trust roots are not
// part of this transaction layer.
type RecoveryProof struct {
	StartupCount  uint64
	SessionHandle Handle
	Generation    uint64
	CPHash        []byte
	Quote         []byte
	Audit         []byte
	Outcome       RecoveryOutcome
}

type principalContextKey struct{}

// WithPrincipal associates the calling subject with a context. When present,
// it must match the principal bound into the session.
func WithPrincipal(ctx context.Context, principal string) context.Context {
	return context.WithValue(ctx, principalContextKey{}, principal)
}

// PrincipalFromContext returns the caller principal previously attached with
// WithPrincipal.
func PrincipalFromContext(ctx context.Context) (string, bool) {
	p, ok := ctx.Value(principalContextKey{}).(string)
	return p, ok
}

// Session is one TPM policy session generation. Authorization is serialized:
// once Authorize installs PolicyCpHash, no other authorization or policy update
// can use the same Session until Submit or Abort finishes.
type Session struct {
	mu            sync.Mutex
	authority     *Authority
	binding       Binding
	policyDigest  []byte
	phase         SessionState
	commitments   Commitments
	prepared      *Prepared
	unknown       *UnknownState
	verifyResp    func(context.Context, ResponseCheck) error
	verifyRecover func(context.Context, RecoveryProof) error
}

func newSession(a *Authority, binding Binding, verifyResponse func(context.Context, ResponseCheck) error, verifyRecovery func(context.Context, RecoveryProof) error) *Session {
	return &Session{
		authority:     a,
		binding:       binding.clone(),
		policyDigest:  append([]byte(nil), binding.PolicyDigest...),
		phase:         StateIdle,
		verifyResp:    verifyResponse,
		verifyRecover: verifyRecovery,
	}
}

// Binding returns a copy of the current binding, including the current
// nonceTPM and policy digest.
func (s *Session) Binding() Binding {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.binding.clone()
	b.PolicyDigest = append([]byte(nil), s.policyDigest...)
	return b
}

// State returns the current transaction state.
func (s *Session) State() SessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.phase
}

// PolicyDigest returns the host's current policy digest for this session.
func (s *Session) PolicyDigest() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.policyDigest...)
}

// Commitments returns an audit snapshot of committed OR, NameHash and cpHash
// state.
func (s *Session) Commitments() Commitments {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.commitments.clone()
}

// Unknown returns details of an unresolved command, if any.
func (s *Session) Unknown() *UnknownState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.unknown.clone()
}

// Authority returns the handle-generation registry that owns this session.
func (s *Session) Authority() *Authority {
	return s.authority
}

// AuthorizeOptions controls optional policy assertions. cpHash is always
// committed. NameHash is optional because cpHash already covers every Name.
type AuthorizeOptions struct {
	NameHash       bool
	OR             *ORSelection
	Auth           *AuthValues
	VerifyResponse func(context.Context, ResponseCheck) error
}

// Authorize computes cpHash from the final command code, canonical Name
// sequence and exact parameter bytes, installs the requested policy
// assertions, verifies the TPM's resulting policy digest, and freezes the
// command. Only authorization values may be supplied afterwards.
func (s *Session) Authorize(ctx context.Context, template CommandTemplate, opts AuthorizeOptions) (*Prepared, error) {
	s.authority.lifecycle.RLock()
	defer s.authority.lifecycle.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.checkBindingLocked(ctx); err != nil {
		return nil, err
	}
	switch s.phase {
	case StateIdle:
	case StateUnknown:
		return nil, ErrSessionUnknown
	case StateAuthorizing, StateAuthorized, StateSubmitting:
		return nil, ErrSessionBusy
	default:
		return nil, ErrSessionExpired
	}

	frozen, err := freezeCommand(template, s.binding.HashAlg)
	if err != nil {
		return nil, err
	}
	or := opts.OR.clone()
	if or != nil {
		if err := validateOR(or, s.binding.HashAlg, s.policyDigest); err != nil {
			return nil, err
		}
	}
	var initialAuth *AuthValues
	if opts.Auth != nil {
		auth := cloneAuth(*opts.Auth)
		if auth.SessionHandle != 0 && auth.SessionHandle != s.binding.SessionHandle {
			return nil, fmt.Errorf("%w: authorization handle %#08x is not the bound session handle %#08x", ErrBindingMismatch, uint32(auth.SessionHandle), uint32(s.binding.SessionHandle))
		}
		initialAuth = &auth
	}

	s.phase = StateAuthorizing
	if or != nil {
		if err := s.policyORLocked(ctx, or); err != nil {
			s.failAuthorizationLocked(err, frozen.CPHash(), !isTPMStatusError(err))
			return nil, err
		}
	}
	if opts.NameHash {
		if err := s.policyNameHashLocked(ctx, frozen); err != nil {
			s.failAuthorizationLocked(err, frozen.CPHash(), !isTPMStatusError(err))
			return nil, err
		}
	}
	if err := s.policyCpHashLocked(ctx, frozen); err != nil {
		s.failAuthorizationLocked(err, frozen.CPHash(), !isTPMStatusError(err))
		return nil, err
	}

	actualDigest, err := s.policyGetDigestLocked(ctx)
	if err != nil {
		s.failAuthorizationLocked(err, frozen.CPHash(), !isTPMStatusError(err))
		return nil, err
	}
	if !equalDigest(actualDigest, s.policyDigest) {
		s.phase = StateInvalid
		return nil, ErrPolicyDigest
	}

	verifyResponse := opts.VerifyResponse
	if verifyResponse == nil {
		verifyResponse = s.verifyResp
	}
	p := &Prepared{
		session:              s,
		generation:           s.binding.Generation,
		frozen:               frozen,
		commitments:          s.commitments.clone(),
		expectedPolicyDigest: append([]byte(nil), s.policyDigest...),
		verifyResponse:       verifyResponse,
	}
	if initialAuth != nil {
		auth := cloneAuth(*initialAuth)
		auth.SessionHandle = s.binding.SessionHandle
		p.auth = auth
		p.authSet = true
	}
	s.prepared = p
	s.phase = StateAuthorized
	return p, nil
}

func (s *Session) checkBindingLocked(ctx context.Context) error {
	if s.authority == nil {
		return ErrInvalidBinding
	}
	if err := s.authority.check(s); err != nil {
		return err
	}
	if principal, ok := PrincipalFromContext(ctx); ok && principal != s.binding.Principal {
		return fmt.Errorf("%w: caller principal %q does not match %q", ErrBindingMismatch, principal, s.binding.Principal)
	}
	return nil
}

func validateOR(or *ORSelection, alg Algorithm, current []byte) error {
	size, err := alg.Size()
	if err != nil {
		return err
	}
	if len(or.Branches) == 0 || or.Selected < 0 || or.Selected >= len(or.Branches) {
		return fmt.Errorf("%w: invalid branch selection", ErrPolicyORBranch)
	}
	for i, branch := range or.Branches {
		if len(branch) != size {
			return fmt.Errorf("%w: branch %d has %d bytes, want %d", ErrPolicyORBranch, i, len(branch), size)
		}
	}
	if subtle.ConstantTimeCompare(or.Branches[or.Selected], current) != 1 {
		return ErrPolicyORBranch
	}
	return nil
}

func (s *Session) policyORLocked(ctx context.Context, or *ORSelection) error {
	payload, err := EncodePolicyORList(or.Branches)
	if err != nil {
		return err
	}
	before := append([]byte(nil), s.policyDigest...)
	next, err := ExtendPolicyDigest(s.binding.HashAlg, before, CCPolicyOR, payload)
	if err != nil {
		return err
	}
	resp, err := s.executeLocked(ctx, CCPolicyOR, []Handle{s.binding.SessionHandle}, payload, nil)
	if err != nil {
		return err
	}
	if resp.Code != 0 {
		return &TPMError{Code: resp.Code, Operation: "PolicyOR"}
	}
	s.policyDigest = next
	s.commitments.OR = &ORCommitment{
		Branches:           cloneBranches(or.Branches),
		Selected:           or.Selected,
		SelectedDigest:     append([]byte(nil), or.Branches[or.Selected]...),
		PolicyDigestBefore: before,
		PolicyDigestAfter:  append([]byte(nil), next...),
	}
	return nil
}

func (s *Session) policyNameHashLocked(ctx context.Context, frozen *FrozenCommand) error {
	nameHash := frozen.NameHash()
	before := append([]byte(nil), s.policyDigest...)
	next, err := ExtendPolicyDigest(s.binding.HashAlg, before, CCPolicyNameHash, nameHash)
	if err != nil {
		return err
	}
	resp, err := s.executeLocked(ctx, CCPolicyNameHash, []Handle{s.binding.SessionHandle}, appendTPM2B(nil, nameHash), nil)
	if err != nil {
		return err
	}
	if resp.Code != 0 {
		return &TPMError{Code: resp.Code, Operation: "PolicyNameHash"}
	}
	s.policyDigest = next
	s.commitments.NameHash = &NameHashCommitment{
		Names:              frozen.Names(),
		NameHash:           nameHash,
		PolicyDigestBefore: before,
		PolicyDigestAfter:  append([]byte(nil), next...),
	}
	return nil
}

func (s *Session) policyCpHashLocked(ctx context.Context, frozen *FrozenCommand) error {
	cpHash := frozen.CPHash()
	before := append([]byte(nil), s.policyDigest...)
	next, err := ExtendPolicyDigest(s.binding.HashAlg, before, CCPolicyCpHash, cpHash)
	if err != nil {
		return err
	}
	resp, err := s.executeLocked(ctx, CCPolicyCpHash, []Handle{s.binding.SessionHandle}, appendTPM2B(nil, cpHash), nil)
	if err != nil {
		return err
	}
	if resp.Code != 0 {
		return &TPMError{Code: resp.Code, Operation: "PolicyCpHash"}
	}
	s.policyDigest = next
	s.commitments.CPHash = &CPHashCommitment{
		CommandCode:        frozen.CommandCode(),
		Handles:            frozen.Handles(),
		Names:              frozen.Names(),
		Parameters:         frozen.Parameters(),
		CPHash:             cpHash,
		PolicyDigestBefore: before,
		PolicyDigestAfter:  append([]byte(nil), next...),
	}
	return nil
}

func (s *Session) policyGetDigestLocked(ctx context.Context) ([]byte, error) {
	resp, err := s.executeLocked(ctx, CCPolicyGetDigest, []Handle{s.binding.SessionHandle}, nil, nil)
	if err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, &TPMError{Code: resp.Code, Operation: "PolicyGetDigest"}
	}
	digest, next, err := readTPM2B(resp.Parameters, 0)
	if err != nil {
		return nil, err
	}
	if next != len(resp.Parameters) {
		return nil, fmt.Errorf("%w: trailing PolicyGetDigest bytes", ErrInvalidResponse)
	}
	return append([]byte(nil), digest...), nil
}

func (s *Session) executeLocked(ctx context.Context, code CommandCode, handles []Handle, parameters []byte, auth []AuthValues) (Response, error) {
	raw, err := MarshalCommand(code, handles, parameters, auth)
	if err != nil {
		return Response{}, err
	}
	respRaw, err := s.authority.transport.Execute(ctx, raw)
	if err != nil {
		return Response{}, err
	}
	return ParseResponse(respRaw)
}

func isTPMStatusError(err error) bool {
	var tpmErr *TPMError
	return errors.As(err, &tpmErr)
}

func (s *Session) failAuthorizationLocked(err error, cpHash []byte, unknown bool) {
	if unknown {
		s.phase = StateUnknown
		s.unknown = &UnknownState{
			SessionHandle: s.binding.SessionHandle,
			Generation:    s.binding.Generation,
			CPHash:        append([]byte(nil), cpHash...),
			Cause:         err.Error(),
			At:            time.Now(),
			PolicyChange:  true,
		}
		s.authority.markUnknown(s.binding.SessionHandle, s.binding.Generation)
		return
	}
	s.phase = StateInvalid
}

func (s *Session) markUnknownLocked(err error) {
	s.phase = StateUnknown
	s.unknown = &UnknownState{
		SessionHandle: s.binding.SessionHandle,
		Generation:    s.binding.Generation,
		CPHash:        s.preparedCPHashLocked(),
		Cause:         err.Error(),
		At:            time.Now(),
	}
	s.authority.markUnknown(s.binding.SessionHandle, s.binding.Generation)
}

func (s *Session) preparedCPHashLocked() []byte {
	if s.prepared == nil || s.prepared.frozen == nil {
		return nil
	}
	return s.prepared.frozen.CPHash()
}

// Reconcile resolves an unknown outcome only after application-supplied quote
// and audit verification succeeds. It never retries the command.
func (s *Session) Reconcile(ctx context.Context, proof RecoveryProof) error {
	s.authority.lifecycle.RLock()
	defer s.authority.lifecycle.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.phase != StateUnknown || s.unknown == nil {
		return ErrNoUnknownOutcome
	}
	if err := s.authority.check(s); err != nil {
		return err
	}
	if proof.StartupCount != s.binding.StartupCount || proof.SessionHandle != s.binding.SessionHandle || proof.Generation != s.binding.Generation {
		return ErrBindingMismatch
	}
	if s.unknown.CPHash != nil && !equalDigest(proof.CPHash, s.unknown.CPHash) {
		return ErrBindingMismatch
	}
	if len(proof.Quote) == 0 || len(proof.Audit) == 0 {
		return ErrRecoveryUnverified
	}
	if s.verifyRecover == nil {
		return ErrRecoveryUnverified
	}
	if err := s.verifyRecover(ctx, proof); err != nil {
		return fmt.Errorf("%w: %v", ErrRecoveryUnverified, err)
	}
	s.unknown = nil
	s.phase = StateConsumed
	s.prepared = nil
	s.authority.clearUnknown(s.binding.SessionHandle, s.binding.Generation)
	return nil
}

// ContextSave saves an idle session generation and invalidates this host
// object. The returned context can only be loaded as a new generation.
func (s *Session) ContextSave(ctx context.Context) ([]byte, error) {
	return s.authority.ContextSave(ctx, s)
}

// Flush flushes the TPM session handle and invalidates this host object.
func (s *Session) Flush(ctx context.Context) error {
	return s.authority.Flush(ctx, s)
}

// Prepared is one frozen command approval. Its command fields have no setters;
// only SetAuth can change the auth area.
type Prepared struct {
	mu                   sync.Mutex
	session              *Session
	generation           uint64
	frozen               *FrozenCommand
	commitments          Commitments
	expectedPolicyDigest []byte
	auth                 AuthValues
	authSet              bool
	submitted            bool
	verifyResponse       func(context.Context, ResponseCheck) error
}

// SetAuth fills only authorization values. Any supplied session handle must be
// the already-bound handle. It cannot alter command code, handles, Names,
// parameters or the cpHash commitment.
func (p *Prepared) SetAuth(auth AuthValues) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.submitted {
		return ErrAlreadySubmitted
	}

	p.session.mu.Lock()
	defer p.session.mu.Unlock()
	if p.session.prepared != p || p.session.phase != StateAuthorized {
		return ErrSessionExpired
	}
	if p.session.binding.Generation != p.generation {
		return ErrGeneration
	}
	if err := p.session.checkBindingLocked(context.Background()); err != nil {
		return err
	}
	if auth.SessionHandle != 0 && auth.SessionHandle != p.session.binding.SessionHandle {
		return ErrBindingMismatch
	}
	auth = cloneAuth(auth)
	auth.SessionHandle = p.session.binding.SessionHandle
	p.auth = auth
	p.authSet = true
	return nil
}

// Template returns a copy of the frozen command.
func (p *Prepared) Template() CommandTemplate {
	if p == nil || p.frozen == nil {
		return CommandTemplate{}
	}
	return p.frozen.cloneTemplate()
}

// CPHash returns the approved cpHash.
func (p *Prepared) CPHash() []byte {
	if p == nil || p.frozen == nil {
		return nil
	}
	return p.frozen.CPHash()
}

// NameHash returns the hash of the approved canonical Name sequence.
func (p *Prepared) NameHash() []byte {
	if p == nil || p.frozen == nil {
		return nil
	}
	return p.frozen.NameHash()
}

// Commitments returns the policy assertions installed for this authorization.
func (p *Prepared) Commitments() Commitments {
	if p == nil {
		return Commitments{}
	}
	return p.commitments.clone()
}

// Wire returns the exact final command packet. It fails until SetAuth has been
// called.
func (p *Prepared) Wire() ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.submitted {
		return nil, ErrAlreadySubmitted
	}
	if !p.authSet {
		return nil, ErrAuthRequired
	}
	p.session.mu.Lock()
	defer p.session.mu.Unlock()
	if p.session.prepared != p || p.session.phase != StateAuthorized {
		return nil, ErrSessionExpired
	}
	if err := p.session.checkBindingLocked(context.Background()); err != nil {
		return nil, err
	}
	return p.frozen.marshal(cloneAuth(p.auth))
}

// Submit verifies the live policy digest one last time, occupies the session
// through response validation, and consumes the session. A transport timeout
// leaves StateUnknown; it is never automatically retried.
func (p *Prepared) Submit(ctx context.Context) (Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.submitted {
		return Response{}, ErrAlreadySubmitted
	}

	s := p.session
	s.authority.lifecycle.RLock()
	defer s.authority.lifecycle.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prepared != p || s.phase != StateAuthorized {
		return Response{}, ErrSessionExpired
	}
	if err := s.checkBindingLocked(ctx); err != nil {
		return Response{}, err
	}
	if s.binding.Generation != p.generation {
		return Response{}, ErrGeneration
	}
	if !p.authSet {
		return Response{}, ErrAuthRequired
	}

	actualDigest, err := s.policyGetDigestLocked(ctx)
	if err != nil {
		s.markUnknownLocked(err)
		return Response{}, fmt.Errorf("%w: %v", ErrOutcomeUnknown, err)
	}
	if !equalDigest(actualDigest, p.expectedPolicyDigest) {
		s.phase = StateInvalid
		s.prepared = nil
		return Response{}, ErrPolicyDigest
	}

	wire, err := p.frozen.marshal(cloneAuth(p.auth))
	if err != nil {
		s.phase = StateInvalid
		return Response{}, err
	}
	p.submitted = true
	s.phase = StateSubmitting

	respRaw, err := s.authority.transport.Execute(ctx, wire)
	if err != nil {
		s.markUnknownLocked(err)
		return Response{}, fmt.Errorf("%w: %v", ErrOutcomeUnknown, err)
	}
	resp, err := ParseResponse(respRaw)
	if err != nil {
		s.markUnknownLocked(err)
		return Response{}, fmt.Errorf("%w: %v", ErrOutcomeUnknown, err)
	}
	if resp.Code != 0 {
		s.phase = StateConsumed
		s.prepared = nil
		return resp, &TPMError{Code: resp.Code, Operation: "authorized command"}
	}
	if resp.Tag != TagSessions || len(resp.Auth) == 0 {
		err := fmt.Errorf("%w: missing authorization response", ErrInvalidResponse)
		s.markUnknownLocked(err)
		return Response{}, fmt.Errorf("%w: %v", ErrOutcomeUnknown, err)
	}
	nonceTPM := resp.Auth[0].NonceTPM
	size, algErr := s.binding.HashAlg.Size()
	if algErr != nil || len(nonceTPM) != size {
		err := fmt.Errorf("%w: response nonceTPM has %d bytes, want %d", ErrInvalidResponse, len(nonceTPM), size)
		s.markUnknownLocked(err)
		return Response{}, fmt.Errorf("%w: %v", ErrOutcomeUnknown, err)
	}
	if p.verifyResponse != nil {
		check := ResponseCheck{
			CPHash:       p.frozen.CPHash(),
			PolicyDigest: append([]byte(nil), p.expectedPolicyDigest...),
			NonceTPM:     append([]byte(nil), nonceTPM...),
			Auth:         cloneAuth(p.auth),
			Command:      p.frozen.cloneTemplate(),
			Response:     resp,
		}
		if err := p.verifyResponse(ctx, check); err != nil {
			s.markUnknownLocked(err)
			return Response{}, fmt.Errorf("%w: response verification failed: %v", ErrOutcomeUnknown, err)
		}
	}
	if resp.Auth[0].Attributes&AttrContinueSession != 0 {
		s.binding.NonceTPM = append([]byte(nil), nonceTPM...)
	}
	s.phase = StateConsumed
	s.prepared = nil
	return resp, nil
}

// Abort releases an authorized but unsubmitted transaction. Because the TPM's
// policy digest already contains the cpHash assertion, the session cannot be
// safely reused and is marked expired.
func (p *Prepared) Abort() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.submitted {
		return ErrAlreadySubmitted
	}
	s := p.session
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prepared != p {
		return nil
	}
	if s.phase != StateAuthorized {
		return nil
	}
	s.prepared = nil
	s.phase = StateExpired
	return nil
}

// Close is an alias for Abort.
func (p *Prepared) Close() error {
	return p.Abort()
}

func cloneAuth(a AuthValues) AuthValues {
	a.NonceCaller = append([]byte(nil), a.NonceCaller...)
	a.HMAC = append([]byte(nil), a.HMAC...)
	return a
}

func cloneBranches(in [][]byte) [][]byte {
	out := make([][]byte, len(in))
	for i := range in {
		out[i] = append([]byte(nil), in[i]...)
	}
	return out
}

func decodeU32(data []byte) (uint32, error) {
	if len(data) != 4 {
		return 0, fmt.Errorf("%w: expected 4-byte value", ErrInvalidResponse)
	}
	return binary.BigEndian.Uint32(data), nil
}
