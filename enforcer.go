package netsec

import (
	"fmt"
	"sync"
	"time"
)

// Enforcer evaluates intents against token-bound policies before the
// orchestrator executes them, and audits intent executions and policy
// changes. It is safe for concurrent use.
type Enforcer struct {
	mu       sync.RWMutex
	tokens   map[string]Token
	policies map[string]Policy
	sink     AuditSink
	now      func() time.Time
}

// NewEnforcer builds an Enforcer that audits to sink. A nil sink
// disables auditing.
func NewEnforcer(sink AuditSink) *Enforcer {
	return &Enforcer{
		tokens:   make(map[string]Token),
		policies: make(map[string]Policy),
		sink:     sink,
		now:      time.Now,
	}
}

// SetClock overrides the time source; mainly for tests.
func (e *Enforcer) SetClock(now func() time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.now = now
}

// RegisterToken adds or replaces a token in the store.
func (e *Enforcer) RegisterToken(t Token) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tokens[t.ID] = t
}

// RevokeToken marks a token as revoked.
func (e *Enforcer) RevokeToken(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if t, ok := e.tokens[id]; ok {
		t.Revoked = true
		e.tokens[id] = t
	}
}

// UpsertPolicy adds or replaces a policy. The change is authorized
// against actorTokenID: the actor must hold a token whose policies grant
// OpPolicyWrite in the target policy's namespace. Every attempt is
// audited as a policy change.
func (e *Enforcer) UpsertPolicy(actorTokenID string, p Policy) Decision {
	intent := Intent{
		TokenID:         actorTokenID,
		Namespace:       p.Namespace,
		ServiceIdentity: p.ServiceIdentity,
		Operation:       OpPolicyWrite,
	}
	decision := e.authorize(intent)

	change := "upsert"
	if !decision.Allowed {
		change = "upsert_rejected"
	} else {
		e.mu.Lock()
		e.policies[p.ID] = p
		e.mu.Unlock()
	}
	e.emit(AuditEvent{
		Kind:     AuditPolicyChange,
		At:       e.clock()(),
		TokenID:  actorTokenID,
		Actor:    actorTokenID,
		PolicyID: p.ID,
		Change:   change,
		Decision: &decision,
		Detail:   decision.Detail,
	})
	return decision
}

// DeletePolicy removes a policy under the same authorization rules as
// UpsertPolicy and audits the change.
func (e *Enforcer) DeletePolicy(actorTokenID, policyID string) Decision {
	e.mu.RLock()
	p, ok := e.policies[policyID]
	e.mu.RUnlock()
	if !ok {
		return Decision{Reason: DenyNoPolicy, Detail: "policy not found: " + policyID}
	}
	intent := Intent{
		TokenID:         actorTokenID,
		Namespace:       p.Namespace,
		ServiceIdentity: p.ServiceIdentity,
		Operation:       OpPolicyWrite,
	}
	decision := e.authorize(intent)
	change := "delete"
	if !decision.Allowed {
		change = "delete_rejected"
	} else {
		e.mu.Lock()
		delete(e.policies, policyID)
		e.mu.Unlock()
	}
	e.emit(AuditEvent{
		Kind:     AuditPolicyChange,
		At:       e.clock()(),
		TokenID:  actorTokenID,
		Actor:    actorTokenID,
		PolicyID: policyID,
		Change:   change,
		Decision: &decision,
		Detail:   decision.Detail,
	})
	return decision
}

// Authorize evaluates an intent without executing it and audits the
// evaluation.
func (e *Enforcer) Authorize(intent Intent) Decision {
	decision := e.authorize(intent)
	i := intent
	e.emit(AuditEvent{
		Kind:     AuditIntent,
		At:       e.clock()(),
		TokenID:  intent.TokenID,
		Intent:   &i,
		Decision: &decision,
		Detail:   decision.Detail,
	})
	return decision
}

// Execute authorizes the intent and, when allowed, invokes run. The
// outcome is audited either way; a denied intent never reaches run.
func (e *Enforcer) Execute(intent Intent, run func() error) (Decision, error) {
	decision := e.Authorize(intent)
	if !decision.Allowed {
		return decision, decision
	}
	if run == nil {
		return decision, nil
	}
	if err := run(); err != nil {
		return decision, fmt.Errorf("netsec: intent execution failed: %w", err)
	}
	return decision, nil
}

// authorize performs the per-dimension match: token existence, expiry,
// revocation, then namespace, service identity and operation against
// every bound policy.
func (e *Enforcer) authorize(intent Intent) Decision {
	e.mu.RLock()
	defer e.mu.RUnlock()

	token, ok := e.tokens[intent.TokenID]
	if !ok {
		return Decision{Reason: DenyTokenUnknown, Detail: "unknown token: " + intent.TokenID}
	}
	if token.Revoked {
		return Decision{Reason: DenyTokenRevoked, Detail: "token revoked: " + token.ID}
	}
	if now := e.now(); !token.ExpiresAt.IsZero() && !now.Before(token.ExpiresAt) {
		return Decision{
			Reason: DenyTokenExpired,
			Detail: fmt.Sprintf("token %s expired at %s", token.ID, token.ExpiresAt.UTC().Format(time.RFC3339)),
		}
	}

	sawNamespace := false
	sawIdentity := false
	for _, pid := range token.PolicyIDs {
		p, ok := e.policies[pid]
		if !ok {
			continue
		}
		if p.allows(intent.Namespace, intent.ServiceIdentity, intent.Operation) {
			return Decision{Allowed: true, Detail: "matched policy " + p.ID}
		}
		if p.Namespace == intent.Namespace {
			sawNamespace = true
			if p.matchesIdentity(intent.ServiceIdentity) {
				sawIdentity = true
			}
		}
	}

	switch {
	case sawIdentity:
		return Decision{
			Reason: DenyPrivilegeEscal,
			Detail: fmt.Sprintf("operation %q not granted for %s/%s", intent.Operation, intent.Namespace, intent.ServiceIdentity),
		}
	case sawNamespace:
		return Decision{
			Reason: DenyIdentity,
			Detail: fmt.Sprintf("service identity %q not covered in namespace %q", intent.ServiceIdentity, intent.Namespace),
		}
	default:
		return Decision{
			Reason: DenyNamespace,
			Detail: fmt.Sprintf("namespace %q not covered by token %s", intent.Namespace, token.ID),
		}
	}
}

func (e *Enforcer) emit(ev AuditEvent) {
	if e.sink != nil {
		e.sink.Record(ev)
	}
}

func (e *Enforcer) clock() func() time.Time {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.now
}
