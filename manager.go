package netsec

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// Manager publishes immutable policy snapshots atomically. A request always
// evaluates exactly one compiled policy, even while a policy change is in
// flight. Continuation tokens are bound to the policy fingerprint and signing
// key, so tokens from an old policy are rejected rather than applied to the new
// policy.
type Manager struct {
	current atomic.Pointer[compiledPolicy]
	mu      sync.Mutex
}

// NewManager compiles a JSON policy and returns a manager that publishes it.
func NewManager(document []byte) (*Manager, error) {
	policy, err := NewPolicy(document)
	if err != nil {
		return nil, err
	}
	return NewManagerWithPolicy(policy)
}

// NewManagerWithPolicy returns a manager for an already compiled policy.
func NewManagerWithPolicy(policy *Policy) (*Manager, error) {
	if policy == nil {
		return nil, fmt.Errorf("%w: nil policy", ErrInvalidPolicy)
	}
	if err := policy.Prepare(); err != nil {
		return nil, err
	}
	cp, err := policy.snapshot()
	if err != nil {
		return nil, err
	}
	manager := &Manager{}
	manager.current.Store(cp)
	return manager, nil
}

// SetPolicy compiles document and publishes it. If compilation fails, the
// previous policy remains active.
func (m *Manager) SetPolicy(document []byte) error {
	policy, err := NewPolicy(document)
	if err != nil {
		return err
	}
	return m.SetPolicyObject(policy)
}

// SetPolicyObject publishes an already decoded policy. If compilation fails,
// the previous policy remains active.
func (m *Manager) SetPolicyObject(policy *Policy) error {
	if m == nil {
		return fmt.Errorf("%w: nil manager", ErrInvalidPolicy)
	}
	if policy == nil {
		return fmt.Errorf("%w: nil policy", ErrInvalidPolicy)
	}
	if err := policy.Prepare(); err != nil {
		return err
	}
	cp, err := policy.snapshot()
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.current.Store(cp)
	m.mu.Unlock()
	return nil
}

// Replace is an alias for SetPolicy.
func (m *Manager) Replace(document []byte) error {
	return m.SetPolicy(document)
}

func (m *Manager) snapshot() (*compiledPolicy, error) {
	if m == nil {
		return nil, fmt.Errorf("%w: nil manager", ErrInvalidPolicy)
	}
	cp := m.current.Load()
	if cp == nil {
		return nil, fmt.Errorf("%w: manager has no policy", ErrInvalidPolicy)
	}
	return cp, nil
}

// Fingerprint returns the active policy fingerprint.
func (m *Manager) Fingerprint() string {
	cp, err := m.snapshot()
	if err != nil {
		return ""
	}
	return cp.digest
}

// Authorize evaluates the active policy snapshot.
func (m *Manager) Authorize(req Request) Decision {
	cp, err := m.snapshot()
	if err != nil {
		return Decision{Effect: EffectDeny, Reason: "manager has no policy", Err: err}
	}
	return cp.authorize(req, nowFunc())
}

// Evaluate is an alias for Authorize.
func (m *Manager) Evaluate(req Request) Decision {
	return m.Authorize(req)
}

// Allowed is a boolean convenience wrapper around Authorize.
func (m *Manager) Allowed(req Request) bool {
	return m.Authorize(req).Allowed
}

// Check returns nil when req is allowed by the active policy.
func (m *Manager) Check(req Request) error {
	decision := m.Authorize(req)
	if decision.Allowed {
		return nil
	}
	cause := decision.Err
	if cause == nil {
		cause = ErrAccessDenied
	}
	return &AccessDeniedError{
		Action: req.Action,
		Bucket: req.Bucket,
		Key:    req.Key,
		Sid:    decision.Sid,
		Reason: decision.Reason,
		Cause:  cause,
	}
}

// AllowedKeys filters keys through the active policy's object-read rules.
func (m *Manager) AllowedKeys(req Request, keys []string) []string {
	cp, err := m.snapshot()
	if err != nil {
		return nil
	}
	return cp.allowedKeys(req, keys)
}

// FilterObjects filters objects through the active policy's object-read rules.
func (m *Manager) FilterObjects(req Request, objects []Object) []Object {
	cp, err := m.snapshot()
	if err != nil {
		return nil
	}
	return cp.filterObjects(req, objects)
}

// List performs a secure listing against the active policy snapshot.
func (m *Manager) List(ctx context.Context, req ListRequest, source ObjectSource) (ListResult, error) {
	cp, err := m.snapshot()
	if err != nil {
		return emptyListResult(req), err
	}
	return cp.list(ctx, req, source)
}
