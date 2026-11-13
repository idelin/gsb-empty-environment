package netsec

import (
	"context"
	"sync"
	"time"
)

type callKey struct {
	principalID string
	requestID   string
}

type callState struct {
	fingerprint string
	done        chan struct{}
	completed   bool

	decision    Decision
	credential  *IssuedCredential
	err         error
	invalidated bool
}

// Broker authorizes assume requests and mediates the external STS callback.
type Broker struct {
	registry *Registry
	sts      STS
	clock    Clock

	mu     sync.Mutex
	calls  map[callKey]*callState
	audit  []Decision
	closed bool
}

// Option configures a Broker.
type Option func(*Broker)

// WithClock replaces the broker's clock. It is primarily useful for
// deterministic expiration tests.
func WithClock(clock Clock) Option {
	return func(b *Broker) {
		if clock != nil {
			b.clock = clock
		}
	}
}

// NewBroker creates an authorization broker. sts may be nil when the host only
// wants to use Authorize for policy evaluation; Assume then returns ErrNoSTS.
func NewBroker(registry *Registry, sts STS, opts ...Option) *Broker {
	if registry == nil {
		registry = NewRegistry()
	}
	b := &Broker{
		registry: registry,
		sts:      sts,
		clock:    systemClock{},
		calls:    make(map[callKey]*callState),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(b)
		}
	}
	return b
}

// Authorize evaluates an assume request without calling STS. It records the
// decision, including denials, in the audit trail.
func (b *Broker) Authorize(auth AuthContext, req AssumeRequest) (Decision, error) {
	if b == nil {
		return Decision{}, ErrInvalidRequest
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return Decision{}, ErrBrokerClosed
	}
	b.mu.Unlock()

	decision, err := b.evaluate(auth, req)
	if decision.Digest != "" || decision.PrincipalID != "" {
		b.mu.Lock()
		b.audit = append(b.audit, decision)
		b.mu.Unlock()
	}
	return decision, err
}

// Assume authorizes the request, calls STS at most once for a successful
// (principal, request ID) pair, validates the returned credential, and returns
// a defensive copy. Concurrent identical calls share one decision and one STS
// invocation. Reusing a request ID with different parameters is rejected, even
// after the original call completed.
func (b *Broker) Assume(ctx context.Context, auth AuthContext, req AssumeRequest) (*IssuedCredential, error) {
	if b == nil {
		return nil, ErrInvalidRequest
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validText(auth.PrincipalID, maxPrincipalIDBytes) {
		return nil, deny("unauthenticated", ErrUnauthenticated, "missing authenticated requester")
	}
	if err := validateAssumeRequest(req); err != nil {
		return nil, err
	}
	if len(req.SessionName) > MaxSessionNameBytes {
		return nil, deny("invalid_session_name", ErrInvalidSessionName,
			"session name must be 1..64 bytes of safe printable ASCII")
	}

	fingerprint := requestFingerprint(auth.PrincipalID, req)
	key := callKey{principalID: auth.PrincipalID, requestID: req.RequestID}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, ErrBrokerClosed
	}
	if entry, exists := b.calls[key]; exists {
		if !constantTimeStringEqual(entry.fingerprint, fingerprint) {
			b.mu.Unlock()
			return nil, ErrRequestConflict
		}
		// Auth-context delegation can be revoked independently of tenant
		// configuration. Re-check it before replays so a cached credential is
		// not returned after the principal loses the tenant.
		if !auth.AllowsTenant(req.TenantID) {
			b.mu.Unlock()
			return nil, deny("tenant_not_delegated", ErrTenantNotAllowed,
				"tenant is no longer present in the authenticated principal context")
		}
		if entry.completed {
			credential, err := b.completedResultLocked(entry)
			b.mu.Unlock()
			return credential, err
		}
		done := entry.done
		b.mu.Unlock()

		select {
		case <-done:
			b.mu.Lock()
			credential, err := b.completedResultLocked(entry)
			b.mu.Unlock()
			return credential, err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	entry := &callState{
		fingerprint: fingerprint,
		done:        make(chan struct{}),
	}
	b.calls[key] = entry
	b.mu.Unlock()

	return b.execute(ctx, entry, auth, req)
}

func (b *Broker) execute(ctx context.Context, entry *callState, auth AuthContext, req AssumeRequest) (*IssuedCredential, error) {
	decision, err := b.evaluate(auth, req)
	if err != nil {
		b.finishFailure(entry, decision, err)
		return nil, err
	}
	if b.sts == nil {
		err = deny("sts_unavailable", ErrNoSTS, "no STS callback is configured")
		b.finishFailure(entry, decision, err)
		return nil, err
	}

	stsReq := STSRequest{
		TenantID:         decision.TenantID,
		RoleID:           decision.RoleID,
		ExternalID:       decision.ExternalID,
		SourceResource:   decision.SourceResource,
		SessionName:      decision.SessionName,
		DurationSeconds:  decision.EffectiveDurationSeconds,
		Audience:         decision.Audience,
		SourceIdentity:   decision.SourceIdentity,
		SessionID:        decision.SessionID,
		ParametersDigest: decision.ParametersDigest,
		PolicyDigest:     decision.PolicyDigest,
		DecisionID:       decision.Digest,
		NotAfter:         decision.NotAfter,
	}

	callbackCredential, callbackErr := b.sts.AssumeRole(ctx, stsReq)
	if callbackErr != nil {
		if callbackCredential != nil {
			callbackCredential.Destroy()
		}
		err := deny("sts_error", ErrSTSFailure, "%v", callbackErr)
		b.finishFailure(entry, decision, err)
		return nil, err
	}
	if callbackCredential == nil {
		err := deny("sts_error", ErrSTSFailure, "STS returned a nil credential")
		b.finishFailure(entry, decision, err)
		return nil, err
	}
	if err := validateSTSCredential(decision, callbackCredential, b.now()); err != nil {
		callbackCredential.Destroy()
		b.finishFailure(entry, decision, err)
		return nil, err
	}

	issued := issuedFromDecision(decision, callbackCredential)

	b.mu.Lock()
	if b.closed {
		issued.Destroy()
		callbackCredential.Destroy()
		err := deny("broker_closed", ErrBrokerClosed, "broker closed before publication")
		b.finishFailureLocked(entry, decision, err)
		b.mu.Unlock()
		return nil, err
	}
	if !b.commitPublicationLocked(entry, decision, issued) {
		issued.Destroy()
		callbackCredential.Destroy()
		err := deny("stale_configuration", ErrStaleConfiguration,
			"tenant configuration changed while the STS callback was in flight")
		b.finishFailureLocked(entry, decision, err)
		b.mu.Unlock()
		return nil, err
	}
	result := issued.clone()
	b.mu.Unlock()
	return result, nil
}

// commitPublicationLocked publishes an in-flight result while holding the
// registry read lock. Registry mutations take the write lock, so a generation
// change either happens-before this commit or observes the published entry.
func (b *Broker) commitPublicationLocked(entry *callState, decision Decision, issued *IssuedCredential) bool {
	b.registry.mu.RLock()
	defer b.registry.mu.RUnlock()
	if !b.generationCurrentLocked(decision.TenantID, decision.TenantGeneration) {
		return false
	}
	entry.decision = decision
	entry.credential = issued
	entry.err = nil
	entry.completed = true
	b.audit = append(b.audit, decision)
	close(entry.done)
	return true
}

func (b *Broker) finishFailure(entry *callState, decision Decision, err error) {
	b.mu.Lock()
	b.finishFailureLocked(entry, decision, err)
	b.mu.Unlock()
}

func (b *Broker) finishFailureLocked(entry *callState, decision Decision, err error) {
	if decision.Allowed {
		decision.Allowed = false
		decision.DenyCode = denialCode(err)
		decision.DenyReason = err.Error()
		decision.Digest = computeDecisionDigest(decision)
	} else if decision.Digest == "" && decision.PrincipalID != "" {
		decision.DenyCode = denialCode(err)
		decision.DenyReason = err.Error()
		decision.Digest = computeDecisionDigest(decision)
	}
	entry.decision = decision
	entry.err = err
	entry.completed = true
	if decision.Digest != "" || decision.PrincipalID != "" {
		b.audit = append(b.audit, decision)
	}
	close(entry.done)
}

// completedResultLocked must be called with b.mu held and only after the entry
// has completed. It rechecks the configuration generation before releasing a
// cached credential.
func (b *Broker) completedResultLocked(entry *callState) (*IssuedCredential, error) {
	b.registry.mu.RLock()
	defer b.registry.mu.RUnlock()

	if entry.credential != nil && !b.generationCurrentLocked(entry.decision.TenantID, entry.decision.TenantGeneration) {
		entry.credential.Destroy()
		entry.credential = nil
		if !entry.invalidated {
			stale := entry.decision
			stale.Allowed = false
			stale.DenyCode = "stale_configuration"
			stale.DenyReason = "tenant configuration changed after the credential was cached"
			stale.Digest = computeDecisionDigest(stale)
			entry.decision = stale
			b.audit = append(b.audit, stale)
			entry.invalidated = true
		}
		entry.err = deny("stale_configuration", ErrStaleConfiguration,
			"tenant configuration changed after the credential was cached")
	}
	if entry.err != nil {
		return nil, entry.err
	}
	if entry.credential == nil {
		return nil, ErrStaleConfiguration
	}
	return entry.credential.clone(), nil
}

func (b *Broker) evaluate(auth AuthContext, req AssumeRequest) (Decision, error) {
	if !validText(auth.PrincipalID, maxPrincipalIDBytes) {
		return Decision{}, deny("unauthenticated", ErrUnauthenticated, "missing authenticated requester")
	}
	if err := validateAssumeRequest(req); err != nil {
		return Decision{}, err
	}

	base, err := b.baseDecision(auth, req)
	if err != nil {
		return Decision{}, err
	}
	if !validSessionName(req.SessionName) {
		return denyDecision(base, "invalid_session_name", ErrInvalidSessionName,
			"session name must be 1..64 bytes of safe printable ASCII")
	}
	if req.DurationSeconds <= 0 {
		return denyDecision(base, "invalid_duration", ErrInvalidDuration,
			"session duration must be positive")
	}
	if !auth.AllowsTenant(req.TenantID) {
		return denyDecision(base, "tenant_not_delegated", ErrTenantNotAllowed,
			"tenant is not present in the authenticated principal context")
	}

	snapshot, ok := b.registry.Snapshot(req.TenantID)
	if !ok {
		return denyDecision(base, "unknown_tenant", ErrUnknownTenant,
			"tenant is not registered")
	}
	base.PolicyDigest = snapshot.Record.PolicyDigest
	base.TenantGeneration = snapshot.Generation
	base.Audience = snapshot.Record.Audience
	base.SourceIdentity = auth.PrincipalID

	if req.RoleID != snapshot.Record.RoleID {
		return denyDecision(base, "role_mismatch", ErrRoleMismatch,
			"requested role does not exactly equal the tenant registration")
	}
	if !constantTimeStringEqual(req.ExternalID, snapshot.Record.ExternalID) {
		return denyDecision(base, "external_id_mismatch", ErrExternalIDMismatch,
			"external ID does not match the tenant registration")
	}
	if !snapshot.AllowsResource(req.SourceResource) {
		return denyDecision(base, "resource_not_allowed", ErrResourceNotAllowed,
			"source resource is not in the tenant's allowed set")
	}
	owner, owned := b.registry.ResourceOwner(req.SourceResource)
	if !owned || owner != req.TenantID {
		return denyDecision(base, "resource_owner_mismatch", ErrResourceOwnerMismatch,
			"source resource ownership no longer belongs to the tenant")
	}

	effective := req.DurationSeconds
	if effective > snapshot.Record.MaxSessionSeconds {
		effective = snapshot.Record.MaxSessionSeconds
	}
	notAfter, ok := addSeconds(base.IssuedAt, effective)
	if !ok {
		return denyDecision(base, "invalid_duration", ErrInvalidDuration,
			"effective session duration is outside the supported range")
	}
	base.RequestedDurationSeconds = req.DurationSeconds
	base.EffectiveDurationSeconds = effective
	base.NotAfter = notAfter
	base.Allowed = true
	base.DenyCode = ""
	base.DenyReason = ""
	base.Digest = computeDecisionDigest(base)
	return base, nil
}

func (b *Broker) baseDecision(auth AuthContext, req AssumeRequest) (Decision, error) {
	sessionID, err := newSessionID()
	if err != nil {
		return Decision{}, deny("internal_error", ErrInvalidRequest, "cannot generate session ID: %v", err)
	}
	return Decision{
		RequestID:                req.RequestID,
		PrincipalID:              auth.PrincipalID,
		TenantID:                 req.TenantID,
		RoleID:                   req.RoleID,
		ExternalID:               req.ExternalID,
		SourceResource:           req.SourceResource,
		SessionName:              req.SessionName,
		ParametersDigest:         req.ParametersDigest,
		RequestedDurationSeconds: req.DurationSeconds,
		Audience:                 req.TenantID,
		SourceIdentity:           auth.PrincipalID,
		SessionID:                sessionID,
		IssuedAt:                 b.now(),
	}, nil
}

func denyDecision(decision Decision, code string, err error, format string, args ...any) (Decision, error) {
	wrapped := deny(code, err, format, args...)
	decision.Allowed = false
	decision.DenyCode = code
	decision.DenyReason = wrapped.Error()
	decision.Digest = computeDecisionDigest(decision)
	return decision, wrapped
}

func validateAssumeRequest(req AssumeRequest) error {
	if !validText(req.RequestID, maxRequestIDBytes) {
		return deny("invalid_request", ErrInvalidRequest, "invalid request ID")
	}
	if !validText(req.TenantID, maxTenantIDBytes) {
		return deny("invalid_request", ErrInvalidRequest, "invalid tenant ID")
	}
	if !validText(req.RoleID, maxRoleIDBytes) {
		return deny("invalid_request", ErrInvalidRequest, "invalid role ID")
	}
	if !validText(req.ExternalID, maxExternalIDBytes) {
		return deny("invalid_request", ErrInvalidRequest, "invalid external ID")
	}
	if !validText(req.SourceResource, maxResourceBytes) {
		return deny("invalid_request", ErrInvalidRequest, "invalid source resource")
	}
	if !validText(req.ParametersDigest, maxParametersDigest) {
		return deny("invalid_request", ErrInvalidRequest, "invalid parameters digest")
	}
	return nil
}

func validateSTSCredential(decision Decision, credential *STSCredential, now time.Time) error {
	if credential == nil {
		return deny("credential_mismatch", ErrCredentialMismatch, "nil STS credential")
	}
	if credential.Audience != decision.Audience {
		return deny("credential_mismatch", ErrCredentialMismatch, "credential audience mismatch")
	}
	if credential.RoleID != decision.RoleID {
		return deny("credential_mismatch", ErrCredentialMismatch, "credential role mismatch")
	}
	if credential.SourceIdentity != decision.SourceIdentity {
		return deny("credential_mismatch", ErrCredentialMismatch, "credential source identity mismatch")
	}
	if credential.SessionID != decision.SessionID {
		return deny("credential_mismatch", ErrCredentialMismatch, "credential session ID mismatch")
	}
	if credential.Expiration.IsZero() {
		return deny("credential_mismatch", ErrCredentialMismatch, "credential expiration is missing")
	}
	if !credential.Expiration.After(now) {
		return deny("credential_expired", ErrCredentialExpired, "credential is already expired")
	}
	if credential.Expiration.After(decision.NotAfter) {
		return deny("credential_expiration_exceeds_cap", ErrCredentialMismatch,
			"credential expiration exceeds the authorized cap")
	}
	return nil
}

func issuedFromDecision(decision Decision, credential *STSCredential) *IssuedCredential {
	return &IssuedCredential{
		AccessKeyID:      credential.AccessKeyID,
		SecretAccessKey:  credential.SecretAccessKey,
		SessionToken:     credential.SessionToken,
		Audience:         credential.Audience,
		RoleID:           credential.RoleID,
		SourceIdentity:   credential.SourceIdentity,
		Expiration:       credential.Expiration.UTC(),
		SessionID:        credential.SessionID,
		TenantID:         decision.TenantID,
		PrincipalID:      decision.PrincipalID,
		RequestID:        decision.RequestID,
		SessionName:      decision.SessionName,
		DecisionID:       decision.Digest,
		TenantGeneration: decision.TenantGeneration,
		PolicyDigest:     decision.PolicyDigest,
	}
}

// Decisions returns a snapshot of every decision recorded so far, including
// denials and stale-result rejections.
func (b *Broker) Decisions() []Decision {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Decision, len(b.audit))
	copy(out, b.audit)
	return out
}

// DecisionByID returns the audit decision with the given canonical digest.
func (b *Broker) DecisionByID(id string) (Decision, bool) {
	if b == nil {
		return Decision{}, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, decision := range b.audit {
		if decision.Digest == id {
			return decision, true
		}
	}
	return Decision{}, false
}

// Close destroys cached credentials and prevents new calls from publishing.
// In-flight STS callbacks finish, but their credentials are destroyed instead
// of being returned.
func (b *Broker) Close() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	for _, entry := range b.calls {
		if entry.credential != nil {
			entry.credential.Destroy()
			entry.credential = nil
		}
	}
	return nil
}

func (b *Broker) now() time.Time {
	now := b.clock.Now()
	if now.IsZero() {
		now = time.Now()
	}
	return now.UTC()
}

// generationCurrentLocked requires the caller to hold Registry.mu.
func (b *Broker) generationCurrentLocked(tenantID string, generation uint64) bool {
	state, ok := b.registry.tenants[tenantID]
	return ok && state.generation == generation
}
