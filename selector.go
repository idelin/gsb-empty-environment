package netsec

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	// ErrInvalidRequest is returned when a request lacks a tenant or audience.
	ErrInvalidRequest = errors.New("netsec: request requires tenant and audience")
	// ErrUnknownSource is returned when a source is absent from the graph.
	ErrUnknownSource = errors.New("netsec: unknown credential source")
	// ErrDuplicateSource is returned when two sources share an id.
	ErrDuplicateSource = errors.New("netsec: duplicate credential source")
	// ErrNoCandidate means no eligible credential is available for the request.
	ErrNoCandidate = errors.New("netsec: no eligible credential candidate")
)

// CandidateRanking is the immutable evidence explaining why a source won.
type CandidateRanking struct {
	Source   SourceID
	Identity string
	// Dominance lists, for every other eligible source, the shortest trust
	// path proving that Source dominates it.
	Dominance map[SourceID][]SourceID
}

// Evidence is the fixed audit trail captured by a single refresh. It never
// changes when the underlying sources mutate afterwards.
type Evidence struct {
	Request     Request
	Eligible    []Credential
	Rejected    []Rejection
	Chosen      *CandidateRanking
	EvaluatedAt time.Time
}

// Selection is an atomic, immutable view published by a successful refresh.
type Selection struct {
	Credential Credential
	Evidence   *Evidence
	Generation uint64
}

// ConflictError reports that two or more eligible candidates are maximal
// under the trust partial order, so no selection is safe.
type ConflictError struct {
	Candidates []Credential
	Evidence   *Evidence
}

func (e *ConflictError) Error() string {
	parts := make([]string, 0, len(e.Candidates))
	for _, candidate := range e.Candidates {
		parts = append(parts, string(candidate.Source))
	}
	return fmt.Sprintf("netsec: incomparable credential candidates %s", strings.Join(parts, ", "))
}

// AsConflict extracts a *ConflictError from err when present.
func AsConflict(err error) (*ConflictError, bool) {
	var conflict *ConflictError
	if errors.As(err, &conflict) {
		return conflict, true
	}
	return nil, false
}

// Selector owns a fixed set of sources and publishes at most one Selection.
// Sources can change their credentials concurrently, but refresh evaluation
// always runs against a point-in-time snapshot.
type Selector struct {
	graph   *TrustGraph
	sources map[SourceID]Source
	order   []SourceID
	now     func() time.Time

	refreshMu sync.Mutex
	current   atomic.Pointer[Selection]
	gen       uint64
}

// NewSelector validates that every source belongs to the trust graph.
func NewSelector(graph *TrustGraph, sources []Source) (*Selector, error) {
	if graph == nil {
		return nil, errors.New("netsec: nil trust graph")
	}
	selector := &Selector{
		graph:   graph,
		sources: make(map[SourceID]Source, len(sources)),
		now:     time.Now,
	}
	for _, source := range sources {
		if source == nil {
			return nil, errors.New("netsec: nil credential source")
		}
		id := source.ID()
		if id == "" {
			return nil, ErrEmptySource
		}
		if !graph.Contains(id) {
			return nil, fmt.Errorf("%w: %q", ErrUnknownSource, id)
		}
		if _, exists := selector.sources[id]; exists {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateSource, id)
		}
		selector.sources[id] = source
		selector.order = append(selector.order, id)
	}
	sort.Slice(selector.order, func(i, j int) bool { return selector.order[i] < selector.order[j] })
	return selector, nil
}

// Current returns the latest published selection, or nil before any
// successful refresh.
func (s *Selector) Current() *Selection { return s.current.Load() }

// Refresh snapshots every source once, evaluates the snapshot against the
// trust partial order, and publishes a selection only when a unique maximum
// exists. On conflict or on no candidate the previously published selection
// is retained, so the served identity never silently drifts.
func (s *Selector) Refresh(ctx context.Context, request Request) (*Selection, error) {
	if request.Tenant == "" || request.Audience == "" {
		return nil, ErrInvalidRequest
	}
	if request.Now.IsZero() {
		request.Now = s.now()
	}

	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	snapshot, rejected := s.snapshot(ctx)
	evidence := s.evaluate(request, snapshot, rejected)

	maximals := maximalCandidates(evidence.Eligible, s.graph)
	if len(maximals) == 0 {
		return s.current.Load(), fmt.Errorf(
			"%w for tenant %q audience %q", ErrNoCandidate, request.Tenant, request.Audience)
	}
	if len(maximals) > 1 {
		return s.current.Load(), &ConflictError{Candidates: maximals, Evidence: evidence}
	}

	winner := maximals[0]
	evidence.Chosen = &CandidateRanking{
		Source:    winner.Source,
		Identity:  winner.Identity,
		Dominance: s.dominanceProof(winner.Source, evidence.Eligible),
	}

	s.gen++
	selection := &Selection{
		Credential: winner,
		Evidence:   evidence,
		Generation: s.gen,
	}
	s.current.Store(selection)
	return selection, nil
}

// Evaluate exposes the pure selection logic without publishing state, which
// is useful for callers that want to inspect the evidence before committing.
func (s *Selector) Evaluate(request Request, candidates []Credential) (*CandidateRanking, []Credential, error) {
	if request.Tenant == "" || request.Audience == "" {
		return nil, nil, ErrInvalidRequest
	}
	if request.Now.IsZero() {
		request.Now = s.now()
	}
	eligible := make([]Credential, 0, len(candidates))
	for _, candidate := range candidates {
		if !candidate.NotBefore.IsZero() && request.Now.Before(candidate.NotBefore) {
			continue
		}
		if !candidate.ExpiresAt.IsZero() && !request.Now.Before(candidate.ExpiresAt) {
			continue
		}
		if candidate.Tenant != request.Tenant || candidate.Audience != request.Audience {
			continue
		}
		eligible = append(eligible, candidate.Clone())
	}
	maximals := maximalCandidates(eligible, s.graph)
	switch {
	case len(maximals) == 0:
		return nil, eligible, ErrNoCandidate
	case len(maximals) > 1:
		return nil, eligible, &ConflictError{Candidates: maximals}
	}
	winner := maximals[0]
	return &CandidateRanking{
		Source:    winner.Source,
		Identity:  winner.Identity,
		Dominance: s.dominanceProof(winner.Source, eligible),
	}, eligible, nil
}

// snapshot reads each source exactly once. Sources are read concurrently so a
// slow or blocked source cannot stall the others, then the results are merged
// in the fixed source order. All filtering happens on the captured copies, so
// later source mutations cannot reach the evaluation.
func (s *Selector) snapshot(ctx context.Context) ([]Credential, []Rejection) {
	type readResult struct {
		id         SourceID
		credential *Credential
		err        error
	}

	results := make([]readResult, len(s.order))
	var wg sync.WaitGroup
	for index, id := range s.order {
		wg.Add(1)
		go func(index int, id SourceID) {
			defer wg.Done()
			credential, err := s.sources[id].Credential(ctx)
			results[index] = readResult{id: id, credential: credential, err: err}
		}(index, id)
	}
	wg.Wait()

	snapshot := make([]Credential, 0, len(results))
	rejected := make([]Rejection, 0)
	for _, result := range results {
		if result.err != nil {
			reason := RejectSourceError
			if errors.Is(result.err, ErrRevoked) {
				reason = RejectRevoked
			}
			rejected = append(rejected, Rejection{
				Source: result.id,
				Reason: reason,
				Err:    result.err,
			})
			continue
		}
		if result.credential == nil {
			continue
		}
		credential := *result.credential
		credential.Source = result.id
		snapshot = append(snapshot, credential.Clone())
	}
	return snapshot, rejected
}

func (s *Selector) evaluate(request Request, snapshot []Credential, rejected []Rejection) *Evidence {
	eligible := make([]Credential, 0, len(snapshot))
	for _, candidate := range snapshot {
		switch {
		case !candidate.NotBefore.IsZero() && request.Now.Before(candidate.NotBefore):
			rejected = append(rejected, Rejection{Source: candidate.Source, Reason: RejectNotYetValid})
		case !candidate.ExpiresAt.IsZero() && !request.Now.Before(candidate.ExpiresAt):
			rejected = append(rejected, Rejection{Source: candidate.Source, Reason: RejectExpired})
		case candidate.Tenant != request.Tenant:
			rejected = append(rejected, Rejection{Source: candidate.Source, Reason: RejectTenantMismatch})
		case candidate.Audience != request.Audience:
			rejected = append(rejected, Rejection{Source: candidate.Source, Reason: RejectAudienceMismatch})
		default:
			eligible = append(eligible, candidate)
		}
	}
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].Source < eligible[j].Source })
	sort.Slice(rejected, func(i, j int) bool { return rejected[i].Source < rejected[j].Source })
	return &Evidence{
		Request:     request,
		Eligible:    eligible,
		Rejected:    rejected,
		EvaluatedAt: request.Now,
	}
}

// maximalCandidates returns the eligible candidates no other eligible
// candidate dominates. Iteration order never affects the result.
func maximalCandidates(eligible []Credential, graph *TrustGraph) []Credential {
	maximals := make([]Credential, 0)
	for _, candidate := range eligible {
		dominated := false
		for _, other := range eligible {
			if other.Source != candidate.Source && graph.Dominates(other.Source, candidate.Source) {
				dominated = true
				break
			}
		}
		if !dominated {
			maximals = append(maximals, candidate)
		}
	}
	sort.Slice(maximals, func(i, j int) bool { return maximals[i].Source < maximals[j].Source })
	return maximals
}

func (s *Selector) dominanceProof(winner SourceID, eligible []Credential) map[SourceID][]SourceID {
	proof := make(map[SourceID][]SourceID, len(eligible))
	for _, candidate := range eligible {
		if candidate.Source == winner {
			continue
		}
		if path := s.graph.Path(winner, candidate.Source); path != nil {
			proof[candidate.Source] = path
		}
	}
	return proof
}
