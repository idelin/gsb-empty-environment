package netsec

import (
	"fmt"
	"sync"
	"sync/atomic"
)

var authorizationIDs atomic.Uint64

// Authorization is an unforgeable capability returned only by Registrar. The
// interface has an unexported method, and the executor additionally requires
// the concrete registrar-owned value, so embedding or wrapping an
// Authorization outside this package does not create a usable capability.
type Authorization interface {
	authorization()
	ID() uint64
	Allowed() Label
}

type authorization struct {
	id        uint64
	registrar *Registrar
	request   *Request
	core      *declassifyCore
	allowed   Label
}

func (*authorization) authorization() {}

// ID returns the authorization's process-local identifier.
func (a *authorization) ID() uint64 {
	if a == nil {
		return 0
	}
	return a.id
}

// Allowed returns the sources this authorization may remove.
func (a *authorization) Allowed() Label {
	if a == nil {
		return 0
	}
	return a.allowed
}

type authorizationRecord struct {
	request  *Request
	core     *declassifyCore
	allowed  Label
	consumed bool
}

// Registrar is the trusted authority that issues and consumes
// authorizations. It is safe for concurrent use. Possession of a Registrar is
// the trust boundary: untrusted callers may use issued capabilities only
// through the executor, and cannot manufacture a registrar record.
type Registrar struct {
	mu      sync.Mutex
	records map[*authorization]*authorizationRecord
}

// NewRegistrar creates an empty authorization registrar.
func NewRegistrar() *Registrar {
	return &Registrar{records: make(map[*authorization]*authorizationRecord)}
}

// Authorize issues a one-time authorization for node under request. The
// authorization can remove only sources in allowed. The registrar records the
// request and the node's immutable identity; callers cannot substitute either
// later.
func (r *Registrar) Authorize(req *Request, node *DeclassifyNode, allowed Label) (Authorization, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: nil registrar", ErrMissingAuthorization)
	}
	if req == nil {
		return nil, ErrInvalidRequest
	}
	if node == nil || node.core == nil {
		return nil, fmt.Errorf("%w: nil declassification node", ErrInvalidExpression)
	}

	a := &authorization{
		id:        authorizationIDs.Add(1),
		registrar: r,
		request:   req,
		core:      node.core,
		allowed:   allowed,
	}

	r.mu.Lock()
	if r.records == nil {
		r.records = make(map[*authorization]*authorizationRecord)
	}
	r.records[a] = &authorizationRecord{
		request: req,
		core:    node.core,
		allowed: allowed,
	}
	r.mu.Unlock()
	return a, nil
}

// Consumed reports whether auth has been committed as used. It returns false
// for forged or unknown authorization values.
func (r *Registrar) Consumed(auth Authorization) bool {
	if r == nil {
		return false
	}
	a, ok := auth.(*authorization)
	if !ok || a == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.records[a]
	return ok && a.registrar == r && record.consumed
}

// prepare validates an authorization for this request and node without
// consuming it. Consumption is deferred until commit, so a failed execution
// rolls back without spending the capability.
func (r *Registrar) prepare(auth Authorization, req *Request, node *DeclassifyNode) (*authorization, Label, error) {
	a, ok := auth.(*authorization)
	if !ok || a == nil || r == nil || a.registrar != r {
		return nil, 0, ErrMissingAuthorization
	}
	if req == nil {
		return nil, 0, ErrInvalidRequest
	}
	if node == nil || node.core == nil {
		return nil, 0, ErrInvalidExpression
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.records[a]
	if !ok || record.core != node.core || a.core != node.core {
		allowed := Label(0)
		if ok {
			allowed = record.allowed
		}
		return nil, allowed, ErrAuthorizationMismatch
	}
	if record.request != req || a.request != req {
		return nil, record.allowed, ErrAuthorizationMismatch
	}
	if record.allowed != a.allowed {
		return nil, record.allowed, ErrMissingAuthorization
	}
	if record.consumed {
		return nil, record.allowed, ErrAuthorizationReuse
	}
	return a, record.allowed, nil
}

// commit atomically checks all authorizations and marks them consumed. If any
// authorization is stale, already consumed, or bound elsewhere, no
// authorization is marked, so a commit is all-or-nothing.
func (r *Registrar) commit(req *Request, authorizations []*authorization) error {
	if r == nil {
		return ErrMissingAuthorization
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	seen := make(map[*authorization]struct{}, len(authorizations))
	for _, a := range authorizations {
		if a == nil || a.registrar != r {
			return ErrMissingAuthorization
		}
		record, ok := r.records[a]
		if !ok || record.core != a.core || record.allowed != a.allowed {
			return ErrMissingAuthorization
		}
		if record.request != req || a.request != req {
			return ErrAuthorizationMismatch
		}
		if record.consumed {
			return ErrAuthorizationReuse
		}
		if _, duplicate := seen[a]; duplicate {
			return ErrAuthorizationReuse
		}
		seen[a] = struct{}{}
	}
	for _, a := range authorizations {
		r.records[a].consumed = true
	}
	return nil
}
