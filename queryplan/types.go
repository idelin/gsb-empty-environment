// Package queryplan implements a generation-aware query planner for a
// resolver host.
//
// The host delegates a query for a zone to an ordered set of authoritative
// candidates. While a query is in flight the host may replace that set; each
// replacement is tagged with a strictly increasing delegation generation. A
// plan is pinned to the generation that was current when the query started, so
// answers returned by its in-flight candidates can only complete the original
// request. Cache writes (and referrals that install a new delegation) are
// gated on the planner's current generation and therefore can never overwrite
// state owned by a newer delegation.
package queryplan

import (
	"context"
	"time"
)

// Candidate is one authoritative server the planner may ask.
type Candidate struct {
	// Name uniquely identifies the candidate within a delegation.
	Name string
	// Addr is the transport-level address of the candidate.
	Addr string
}

// Delegation is an authoritative server set tagged with a generation.
type Delegation struct {
	// Generation is a host-assigned, strictly increasing sequence number.
	Generation uint64
	// Candidates are tried in order, concurrently, until one answers.
	Candidates []Candidate
}

// Query is the question a host asks the planner.
type Query struct {
	// Name is the query target, e.g. a DNS question name.
	Name string
	// QType is the query type, e.g. a DNS rrtype number.
	QType uint16
}

// Response is an answer returned by an Upstream.
type Response struct {
	// Answer is the transport-neutral payload handed back to the host.
	Answer any
	// NewDelegation, when non-nil, asks the host to install a new authoritative
	// server set. It is only installed when the response still belongs to the
	// planner's current generation.
	NewDelegation *Delegation
}

// Upstream abstracts the transport used to reach one candidate.
//
// Implementations must honor ctx: returning promptly with ctx.Err() when the
// context is canceled or its deadline is exceeded.
type Upstream interface {
	Query(ctx context.Context, c Candidate, q Query) (*Response, error)
}

// State classifies the terminal outcome of a single candidate attempt.
type State int

const (
	// StatePending means the candidate has not terminated yet.
	StatePending State = iota
	// StateSuccess means the candidate returned a usable response.
	StateSuccess
	// StateTimedOut means the per-candidate deadline fired first.
	StateTimedOut
	// StateCanceled means the request (or plan-wide) context was canceled.
	StateCanceled
	// StateFailed means the candidate returned a transport/upstream error.
	StateFailed
)

func (s State) String() string {
	switch s {
	case StatePending:
		return "pending"
	case StateSuccess:
		return "success"
	case StateTimedOut:
		return "timeout"
	case StateCanceled:
		return "canceled"
	case StateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// CandidateStatus is the tracked terminal state of one candidate attempt.
type CandidateStatus struct {
	Candidate Candidate
	State     State
	Err       error
	Duration  time.Duration
}

// Result is what Lookup hands back to the host.
type Result struct {
	Query      Query
	Response   *Response
	Generation uint64
	// Candidates holds the outcome of every attempt, in delegation order.
	Candidates []CandidateStatus
	// FromCache is true when the answer was served without any upstream call.
	FromCache bool
	// Elapsed is the total wall time the plan spent, including cache hits.
	Elapsed time.Duration
}

// DefaultCandidateTimeout bounds a single candidate attempt when the host does
// not supply a shorter deadline through the Lookup context.
const DefaultCandidateTimeout = 2 * time.Second

// cacheEntry is one answer remembered for a (name, qtype) tuple.
type cacheEntry struct {
	generation uint64
	response   *Response
}

func cacheKey(q Query) string {
	// qType occupies the high bits so names never collide across types.
	return string(rune(q.QType>>8)) + string(rune(q.QType&0xff)) + "/" + q.Name
}
