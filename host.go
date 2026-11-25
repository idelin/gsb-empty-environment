package credentialhost

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type callState struct {
	fingerprint string
	decision    Decision
	done        chan struct{}
	completed   bool
	delivered   bool
	err         error
	credentials *Credentials
}

// Host coordinates authenticated assume requests, external STS calls,
// credential validation, and per-request-ID idempotency.
type Host struct {
	registry *Registry
	sts      STS
	now      func() time.Time

	mu         sync.Mutex
	calls      map[string]*callState
	unregister func()
	closeOnce  sync.Once
}

// NewHost constructs a credential host. Both dependencies are required.
func NewHost(registry *Registry, sts STS, options ...Option) (*Host, error) {
	if registry == nil {
		return nil, fmt.Errorf("credentialhost: registry is required")
	}
	if sts == nil {
		return nil, fmt.Errorf("credentialhost: STS callback is required")
	}
	host := &Host{
		registry: registry,
		sts:      sts,
		now:      time.Now,
		calls:    make(map[string]*callState),
	}
	for _, option := range options {
		if option != nil {
			option(host)
		}
	}
	host.unregister = registry.addChangeListener(host.invalidateTenant)
	return host, nil
}

// New is an alias for NewHost.
func New(registry *Registry, sts STS, options ...Option) (*Host, error) {
	return NewHost(registry, sts, options...)
}

// Close stops tenant-change notifications. It does not destroy credentials that
// are already owned by callers.
func (h *Host) Close() {
	if h == nil {
		return
	}
	h.closeOnce.Do(func() {
		if h.unregister != nil {
			h.unregister()
		}
	})
}

// Assume authorizes and executes an assume request.
//
// The requester comes only from ctx via WithPrincipal. Authorization is checked
// before any STS callback. Concurrent calls with the same RequestID and the same
// request parameters share one STS callback and one result. Reusing a RequestID
// with different parameters fails with ErrRequestIDConflict. Revocation,
// tenant-config rotation, or resource-ownership transfer makes the request
// ErrStaleRequest, including when an old STS callback completes late.
func (h *Host) Assume(ctx context.Context, request AssumeRequest) (*Credentials, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: context is required", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	principal, ok := PrincipalFromContext(ctx)
	if !ok || !validExactString(principal.Subject) {
		return nil, ErrUnauthenticated
	}
	if err := validateAssumeRequest(request); err != nil {
		return nil, err
	}
	fingerprint := computeRequestFingerprint(request, principal)

	for {
		h.mu.Lock()
		state, exists := h.calls[request.RequestID]
		if exists {
			if state.fingerprint != fingerprint {
				h.mu.Unlock()
				return nil, ErrRequestIDConflict
			}
			if !principal.ownsTenant(request.TenantID) {
				h.mu.Unlock()
				return nil, ErrTenantClaimMismatch
			}

			h.registry.mu.RLock()
			current := h.registry.stillCurrentLocked(state.decision)
			h.registry.mu.RUnlock()
			if !current {
				var credentials *Credentials
				if !state.delivered {
					credentials = state.credentials
				}
				state.credentials = nil
				staleErr := staleRequestError()
				state.err = staleErr
				h.mu.Unlock()
				credentials.Destroy()
				return nil, staleErr
			}
			if state.completed {
				credentials := state.credentials
				err := state.err
				state.delivered = true
				h.mu.Unlock()
				return credentials, err
			}
			done := state.done
			h.mu.Unlock()

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-done:
				continue
			}
		}

		now := h.now()
		h.registry.mu.RLock()
		decision, err := h.registry.authorizeLocked(request, principal, now)
		h.registry.mu.RUnlock()
		if err != nil {
			h.mu.Unlock()
			return nil, err
		}
		state = &callState{
			fingerprint: fingerprint,
			decision:    decision,
			done:        make(chan struct{}),
		}
		h.calls[request.RequestID] = state
		h.mu.Unlock()

		return h.execute(ctx, request, state)
	}
}

func (h *Host) execute(ctx context.Context, request AssumeRequest, state *callState) (*Credentials, error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return h.finish(request.RequestID, state.decision, nil, ctxErr)
	}
	if !h.stillCurrent(state.decision) {
		return h.finish(request.RequestID, state.decision, nil, staleRequestError())
	}

	credentials, err := h.invokeSTS(ctx, request, state.decision)
	if ctxErr := ctx.Err(); ctxErr != nil {
		credentials.Destroy()
		return h.finish(request.RequestID, state.decision, nil, ctxErr)
	}
	if err != nil {
		credentials.Destroy()
		return h.finish(request.RequestID, state.decision, nil, fmt.Errorf("%w: %w", ErrSTSFailure, err))
	}
	if credentials == nil {
		return h.finish(request.RequestID, state.decision, nil, fmt.Errorf("%w: STS returned nil credentials", ErrSTSFailure))
	}
	if err := validateCredentials(credentials, state.decision, h.now()); err != nil {
		credentials.Destroy()
		return h.finish(request.RequestID, state.decision, nil, err)
	}
	credentials.DecisionDigest = state.decision.Digest
	credentials.TenantID = state.decision.TenantID
	credentials.TenantVersion = state.decision.TenantVersion
	credentials.ResourceVersion = state.decision.ResourceVersion
	credentials.RequestID = state.decision.RequestID
	return h.finish(request.RequestID, state.decision, credentials, nil)
}

func (h *Host) stillCurrent(decision Decision) bool {
	h.registry.mu.RLock()
	defer h.registry.mu.RUnlock()
	return h.registry.stillCurrentLocked(decision)
}

func (h *Host) invokeSTS(ctx context.Context, request AssumeRequest, decision Decision) (credentials *Credentials, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			credentials.Destroy()
			credentials = nil
			err = fmt.Errorf("%w: panic: %v", ErrSTSFailure, recovered)
		}
	}()
	return h.sts.AssumeRole(ctx, STSRequest{
		RequestID:   request.RequestID,
		Decision:    cloneDecision(decision),
		SessionName: request.SessionName,
	})
}

func cloneDecision(decision Decision) Decision {
	decision.CanonicalDecision = append([]byte(nil), decision.CanonicalDecision...)
	return decision
}

func (h *Host) finish(requestID string, decision Decision, credentials *Credentials, resultErr error) (*Credentials, error) {
	h.mu.Lock()
	state := h.calls[requestID]

	h.registry.mu.RLock()
	current := h.registry.stillCurrentLocked(decision)
	if current {
		if resultErr == nil {
			state.err = nil
			state.credentials = credentials
		} else {
			state.err = resultErr
			state.credentials = nil
		}
		state.completed = true
		state.delivered = true
		close(state.done)
	}
	h.registry.mu.RUnlock()

	if current {
		finalCredentials := state.credentials
		finalErr := state.err
		h.mu.Unlock()
		return finalCredentials, finalErr
	}

	staleErr := staleRequestError()
	state.err = staleErr
	state.credentials = nil
	h.mu.Unlock()

	credentials.Destroy()

	h.mu.Lock()
	if !state.completed {
		state.completed = true
		close(state.done)
	}
	h.mu.Unlock()
	return nil, staleErr
}

func (h *Host) invalidateTenant(tenantID string) {
	var credentials []*Credentials
	h.mu.Lock()
	for _, state := range h.calls {
		if state.decision.TenantID != tenantID {
			continue
		}
		if state.credentials != nil && !state.delivered {
			credentials = append(credentials, state.credentials)
		}
		state.credentials = nil
		state.err = staleRequestError()
	}
	h.mu.Unlock()

	for _, credential := range credentials {
		credential.Destroy()
	}
}

func validateAssumeRequest(request AssumeRequest) error {
	if !validExactString(request.RequestID) {
		return fmt.Errorf("%w: request ID must be non-empty without surrounding whitespace", ErrInvalidRequest)
	}
	if !validExactString(request.TenantID) {
		return fmt.Errorf("%w: tenant ID must be non-empty without surrounding whitespace", ErrInvalidRequest)
	}
	if !validExactString(request.RoleID) {
		return fmt.Errorf("%w: role ID must be non-empty without surrounding whitespace", ErrInvalidRequest)
	}
	if !validExactString(request.ExternalID) {
		return fmt.Errorf("%w: external ID must be non-empty without surrounding whitespace", ErrInvalidRequest)
	}
	if !validExactString(request.SourceResource) {
		return fmt.Errorf("%w: source resource must be non-empty without surrounding whitespace", ErrInvalidRequest)
	}
	if request.DurationSeconds <= 0 {
		return ErrInvalidDuration
	}
	if !validExactString(request.ParametersDigest) {
		return fmt.Errorf("%w: parameter digest must be non-empty without surrounding whitespace", ErrInvalidRequest)
	}
	return validateSessionName(request.SessionName)
}

func validateCredentials(credentials *Credentials, decision Decision, now time.Time) error {
	if !constantTimeEqual(credentials.Audience, decision.Audience) {
		return fmt.Errorf("%w: audience", ErrCredentialMismatch)
	}
	if credentials.RoleID != decision.RoleID {
		return fmt.Errorf("%w: role ID", ErrCredentialMismatch)
	}
	if !constantTimeEqual(credentials.SourceIdentity, decision.SourceIdentity) {
		return fmt.Errorf("%w: source identity", ErrCredentialMismatch)
	}
	if !constantTimeEqual(credentials.SessionID, decision.SessionID) {
		return fmt.Errorf("%w: session ID", ErrCredentialMismatch)
	}
	if credentials.ExpiresAt.IsZero() {
		return fmt.Errorf("%w: missing expiry", ErrCredentialMismatch)
	}
	if credentials.ExpiresAt.After(decision.NotAfter) {
		return fmt.Errorf("%w: expiry exceeds requested maximum", ErrCredentialMismatch)
	}
	if !credentials.ExpiresAt.After(now) {
		return ErrCredentialExpired
	}
	return nil
}

func staleRequestError() error {
	return fmt.Errorf("%w: tenant generation or resource ownership changed", ErrStaleRequest)
}
