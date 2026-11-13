// Package grantsweep implements differential search over grant state-machine
// designs. A parameterized model captures both the reference semantics (every
// Policy flag off) and common weak implementations (one flag on). BFS then
// enumerates Create/Use/Retire/rotate schedules and reports the shortest trace
// in which a weak model returns an unauthorized success while the reference
// denies.
package grantsweep

import (
	"fmt"
	"time"
)

// Principal is an authenticated caller.
type Principal string

// Operation is a KMS operation.
type Operation string

const (
	// OpDecrypt is the target operation the attacker wants to succeed.
	OpDecrypt Operation = "Decrypt"
	OpSign    Operation = "Sign"
)

// Context is a call/constraint context.
type Context map[string]string

// Status is the grant lifecycle state.
type Status uint8

const (
	StatusPending Status = iota + 1
	StatusActive
	StatusRetired
)

// Grant is the modeled grant record.
type Grant struct {
	GrantID           string
	KeyID             string
	KeyVersion        uint64
	GranteePrincipal  Principal
	RetiringPrincipal Principal
	Operations        []Operation
	Constraints       Context // exact equality
	Subset            Context // required subset, extra keys allowed
	Generation        uint64
	Status            Status
}

func (g *Grant) clone() *Grant {
	cp := *g
	cp.Operations = append([]Operation(nil), g.Operations...)
	cp.Constraints = cloneContext(g.Constraints)
	cp.Subset = cloneContext(g.Subset)
	return &cp
}

func cloneContext(c Context) Context {
	if c == nil {
		return nil
	}
	cp := make(Context, len(c))
	for k, v := range c {
		cp[k] = v
	}
	return cp
}

// Token is the modeled bearer token. In the reference model every token is
// unforgeable and bound to the grant id, its generation and its digest.
type Token struct {
	GrantID    string
	Generation uint64
	// Kind is "pending" or "active".
	Kind   string
	Expiry time.Time
	Owner  Principal
}

type pendingInfo struct {
	requestID string
	creator   Principal
	deadline  time.Time
	allowed   bool
}

type record struct {
	grant   *Grant
	pending *pendingInfo
	tokens  []*Token
}

func cloneToken(t *Token) *Token {
	cp := *t
	return &cp
}

func (r *record) clone() *record {
	cp := &record{grant: r.grant.clone()}
	if r.pending != nil {
		p := *r.pending
		cp.pending = &p
	}
	for _, t := range r.tokens {
		cp.tokens = append(cp.tokens, cloneToken(t))
	}
	return cp
}

// Step is one executed action in a trace.
type Step struct {
	Name string
	// Detail renders the concrete arguments for the human-readable trace.
	Detail string
	// Allowed is the model verdict.
	Allowed bool
}

func (s Step) String() string {
	verdict := "DENY"
	if s.Allowed {
		verdict = "ALLOW"
	}
	return fmt.Sprintf("%-28s %-58s %s", s.Name, s.Detail, verdict)
}
