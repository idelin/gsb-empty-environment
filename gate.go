package netsec

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// Decision is the admission verdict for an early-data request.
type Decision int

const (
	// DecisionReject drops the request: the ticket is invalid, bound
	// to another context, revoked, or superseded.
	DecisionReject Decision = iota
	// DecisionDefer holds the request until the handshake completes;
	// it must not be executed as early data.
	DecisionDefer
	// DecisionAllowEarly permits execution as early data, guarded by
	// the replay cache when a replay key is present.
	DecisionAllowEarly
)

func (d Decision) String() string {
	switch d {
	case DecisionReject:
		return "reject"
	case DecisionDefer:
		return "defer-until-handshake"
	case DecisionAllowEarly:
		return "allow-early"
	default:
		return "unknown"
	}
}

// Request carries the connection- and request-level attributes the
// admission decision depends on.
type Request struct {
	Method         string
	SNI            string
	ALPN           string
	IdempotencyKey string
}

// Admission is the outcome of Gate.AdmitEarly / Gate.AdmitPostHandshake.
type Admission struct {
	Decision Decision
	Ticket   *Ticket
	Reason   string
	// ReplayKey, when non-empty, must be routed through Gate.Execute so
	// duplicates share a single execution.
	ReplayKey string
}

// Gate is the early-data admission component an HTTP/3 gateway calls
// before executing any request that arrived in the first flight.
type Gate struct {
	issuer *Issuer
	policy atomic.Pointer[Policy]
	cache  *ReplayCache
}

// NewGate builds a Gate around an issuer and the initial policy.
func NewGate(issuer *Issuer, pol Policy) *Gate {
	return NewGateWithCache(issuer, pol, NewReplayCache(5*time.Minute))
}

// NewGateWithCache lets the caller control replay retention.
func NewGateWithCache(issuer *Issuer, pol Policy, cache *ReplayCache) *Gate {
	g := &Gate{issuer: issuer, cache: cache}
	g.policy.Store(&pol)
	return g
}

// Policy returns the currently active policy.
func (g *Gate) Policy() Policy {
	return *g.policy.Load()
}

// Tighten atomically bumps the policy version and applies mutate.
// Tickets issued under the previous version stop passing admission, so
// tightening immediately invalidates outstanding tickets.
func (g *Gate) Tighten(mutate func(*Policy)) {
	for {
		old := g.policy.Load()
		next := *old
		next.Version++
		if mutate != nil {
			mutate(&next)
		}
		if g.policy.CompareAndSwap(old, &next) {
			return
		}
	}
}

// AdmitEarly decides whether a request carried in the first flight may
// execute before the handshake completes.
func (g *Gate) AdmitEarly(token string, req Request) Admission {
	t, err := g.issuer.Unseal(token)
	if err != nil {
		return Admission{Decision: DecisionReject, Reason: err.Error()}
	}
	if adm, ok := checkBinding(t, req); !ok {
		return adm
	}
	if t.PolicyVersion != g.policy.Load().Version {
		return Admission{
			Decision: DecisionReject,
			Reason:   "netsec: ticket issued under superseded policy",
		}
	}
	pol := g.policy.Load()
	if IsIdempotentMethod(req.Method) && pol.AllowIdempotentEarly {
		return Admission{
			Decision:  DecisionAllowEarly,
			Ticket:    t,
			ReplayKey: replayKey(t, req),
		}
	}
	if req.IdempotencyKey == "" && pol.RequireIdempotencyKey {
		return Admission{
			Decision: DecisionDefer,
			Ticket:   t,
			Reason:   "netsec: non-idempotent method without idempotency key",
		}
	}
	return Admission{
		Decision:  DecisionAllowEarly,
		Ticket:    t,
		ReplayKey: replayKey(t, req),
	}
}

// AdmitPostHandshake re-validates a deferred (or normally resumed)
// request after the handshake has completed. Method restrictions no
// longer apply, but ticket binding and replay protection still do.
func (g *Gate) AdmitPostHandshake(token string, req Request) Admission {
	t, err := g.issuer.Unseal(token)
	if err != nil {
		return Admission{Decision: DecisionReject, Reason: err.Error()}
	}
	if adm, ok := checkBinding(t, req); !ok {
		return adm
	}
	if t.PolicyVersion != g.policy.Load().Version {
		return Admission{
			Decision: DecisionReject,
			Reason:   "netsec: ticket issued under superseded policy",
		}
	}
	return Admission{
		Decision:  DecisionAllowEarly,
		Ticket:    t,
		ReplayKey: replayKey(t, req),
	}
}

// Execute runs fn under replay protection. When the admission carries
// a replay key, duplicates block on the in-flight execution and share
// its result instead of re-running fn; shared reports that case.
func (g *Gate) Execute(adm Admission, fn func() (any, error)) (result any, shared bool, err error) {
	if adm.Decision == DecisionReject {
		return nil, false, errors.New("netsec: cannot execute a rejected admission: " + adm.Reason)
	}
	if adm.Decision == DecisionDefer {
		return nil, false, errors.New("netsec: cannot execute a deferred admission before handshake completion")
	}
	if adm.ReplayKey == "" {
		res, err := fn()
		return res, false, err
	}
	return g.cache.Do(adm.ReplayKey, fn)
}

func checkBinding(t *Ticket, req Request) (Admission, bool) {
	if !strings.EqualFold(t.SNI, req.SNI) {
		return Admission{
			Decision: DecisionReject,
			Reason:   fmt.Sprintf("netsec: ticket bound to SNI %q, not %q", t.SNI, req.SNI),
		}, false
	}
	if t.ALPN != req.ALPN {
		return Admission{
			Decision: DecisionReject,
			Reason:   fmt.Sprintf("netsec: ticket bound to ALPN %q, not %q", t.ALPN, req.ALPN),
		}, false
	}
	return Admission{}, true
}

// replayKey scopes deduplication to the client identity and the
// one-time idempotency key. Requests without a key get no replay key:
// they are only admitted when their method is idempotent.
func replayKey(t *Ticket, req Request) string {
	if req.IdempotencyKey == "" {
		return ""
	}
	return t.ClientID + "\x00" + req.IdempotencyKey
}
