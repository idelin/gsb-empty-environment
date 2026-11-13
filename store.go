package netsec

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

// Route is one installed route and the host-provided session on which it was
// learned.
type Route struct {
	ID      string
	Path    Path
	Session Session
}

// InstalledRoute is an immutable route/evaluation pair.
type InstalledRoute struct {
	Route      Route
	Evaluation Evaluation
}

// StoreSnapshot is a consistent view of a dataset version and all installed
// routes evaluated against it.
type StoreSnapshot struct {
	DatasetVersion string
	Routes         map[string]InstalledRoute
}

// RouteChange describes a verdict change caused by a dataset switch.
type RouteChange struct {
	ID           string
	Before       Verdict
	After        Verdict
	BeforeReason ReasonCode
	AfterReason  ReasonCode
}

// SwapReport describes the atomic dataset switch.
type SwapReport struct {
	OldVersion  string
	NewVersion  string
	Revalidated []string
	Changes     []RouteChange
}

type storeState struct {
	dataset *Dataset
	routes  map[string]InstalledRoute
}

// RouteStore keeps installed routes and atomically swaps complete dataset
// snapshots. Readers never observe a new dataset paired with old evaluations.
type RouteStore struct {
	mu         sync.Mutex
	state      atomic.Pointer[storeState]
	policy     Policy
	maxPrepend int
}

// Router is an alias for RouteStore.
type Router = RouteStore

// NewRouteStore creates a route store from an immutable dataset snapshot.
func NewRouteStore(dataset *Dataset, options Options) (*RouteStore, error) {
	if dataset == nil {
		return nil, fmt.Errorf("%w: nil dataset", ErrInvalidDataset)
	}
	maxPrepend := options.MaxPrepend
	if maxPrepend <= 0 {
		maxPrepend = DefaultMaxPrepend
	}
	store := &RouteStore{policy: options.Policy, maxPrepend: maxPrepend}
	store.state.Store(&storeState{dataset: dataset, routes: make(map[string]InstalledRoute)})
	return store, nil
}

// NewRouter is a convenience alias for NewRouteStore.
func NewRouter(dataset *Dataset, options Options) (*RouteStore, error) {
	return NewRouteStore(dataset, options)
}

// DatasetVersion returns the currently published datasetVersion.
func (s *RouteStore) DatasetVersion() string {
	if s == nil {
		return ""
	}
	return s.state.Load().dataset.Version()
}

// Upsert installs or replaces a route and evaluates it against the current
// dataset. Invalid paths are retained for monitoring and later re-evaluation.
func (s *RouteStore) Upsert(route Route) (Evaluation, error) {
	if s == nil {
		return Evaluation{Verdict: VerdictInvalid, Accepted: false}, fmt.Errorf("%w: nil store", ErrInvalidDataset)
	}
	if route.ID == "" {
		return Evaluation{Verdict: VerdictInvalid, Accepted: false}, fmt.Errorf("%w: route ID must not be empty", ErrInvalidPath)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current := s.state.Load()
	validator, err := NewValidator(current.dataset, Options{Policy: s.policy, MaxPrepend: s.maxPrepend})
	if err != nil {
		return Evaluation{Verdict: VerdictInvalid, Accepted: false}, err
	}
	evaluation := validator.ValidateRoute(route)

	routes := cloneRouteMap(current.routes)
	routes[route.ID] = InstalledRoute{
		Route: Route{
			ID:      route.ID,
			Path:    clonePath(route.Path),
			Session: route.Session,
		},
		Evaluation: cloneEvaluation(evaluation),
	}
	s.state.Store(&storeState{dataset: current.dataset, routes: routes})
	return cloneEvaluation(evaluation), nil
}

// Remove deletes an installed route.
func (s *RouteStore) Remove(id string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	current := s.state.Load()
	if _, ok := current.routes[id]; !ok {
		return false
	}
	routes := cloneRouteMap(current.routes)
	delete(routes, id)
	s.state.Store(&storeState{dataset: current.dataset, routes: routes})
	return true
}

// Get returns one installed route from a consistent snapshot.
func (s *RouteStore) Get(id string) (InstalledRoute, bool) {
	if s == nil {
		return InstalledRoute{}, false
	}
	installed, ok := s.state.Load().routes[id]
	if !ok {
		return InstalledRoute{}, false
	}
	return cloneInstalled(installed), true
}

// Snapshot returns a consistent copy of the current dataset version and routes.
func (s *RouteStore) Snapshot() StoreSnapshot {
	if s == nil {
		return StoreSnapshot{Routes: map[string]InstalledRoute{}}
	}
	current := s.state.Load()
	routes := make(map[string]InstalledRoute, len(current.routes))
	for id, installed := range current.routes {
		routes[id] = cloneInstalled(installed)
	}
	return StoreSnapshot{DatasetVersion: current.dataset.Version(), Routes: routes}
}

// SwapDataset atomically installs a new dataset snapshot. Routes that contain
// a customer AS whose complete provider set changed are re-evaluated. Other
// routes keep their verdict and hops but are republished under the new
// datasetVersion. The new snapshot becomes visible in one pointer store.
func (s *RouteStore) SwapDataset(next *Dataset) (SwapReport, error) {
	if s == nil {
		return SwapReport{}, fmt.Errorf("%w: nil store", ErrInvalidDataset)
	}
	if next == nil {
		return SwapReport{}, fmt.Errorf("%w: nil dataset", ErrInvalidDataset)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	current := s.state.Load()
	report := SwapReport{OldVersion: current.dataset.Version(), NewVersion: next.Version()}
	changed := current.dataset.changedCustomers(next)
	validator, err := NewValidator(next, Options{Policy: s.policy, MaxPrepend: s.maxPrepend})
	if err != nil {
		return SwapReport{}, err
	}

	routes := make(map[string]InstalledRoute, len(current.routes))
	for id, installed := range current.routes {
		affected := routeAffected(installed, changed)
		nextEvaluation := cloneEvaluation(installed.Evaluation)
		if affected {
			nextEvaluation = validator.ValidateRoute(installed.Route)
			report.Revalidated = append(report.Revalidated, id)
		}
		nextEvaluation.DatasetVersion = next.Version()

		if installed.Evaluation.Verdict != nextEvaluation.Verdict {
			report.Changes = append(report.Changes, RouteChange{
				ID:           id,
				Before:       installed.Evaluation.Verdict,
				After:        nextEvaluation.Verdict,
				BeforeReason: issueReason(installed.Evaluation.FirstIssue),
				AfterReason:  issueReason(nextEvaluation.FirstIssue),
			})
		}
		routes[id] = InstalledRoute{Route: cloneRoute(installed.Route), Evaluation: nextEvaluation}
	}

	sort.Strings(report.Revalidated)
	sort.Slice(report.Changes, func(i, j int) bool { return report.Changes[i].ID < report.Changes[j].ID })
	s.state.Store(&storeState{dataset: next, routes: routes})
	return report, nil
}

func routeAffected(installed InstalledRoute, changed map[ASN]struct{}) bool {
	if len(changed) == 0 {
		return false
	}
	if len(installed.Evaluation.Path.ASNs) == 0 {
		return true
	}
	for _, asn := range installed.Evaluation.Path.ASNs {
		if _, ok := changed[asn]; ok {
			return true
		}
	}
	return false
}

func cloneRouteMap(source map[string]InstalledRoute) map[string]InstalledRoute {
	result := make(map[string]InstalledRoute, len(source)+1)
	for id, installed := range source {
		result[id] = cloneInstalled(installed)
	}
	return result
}

func cloneInstalled(installed InstalledRoute) InstalledRoute {
	return InstalledRoute{Route: cloneRoute(installed.Route), Evaluation: cloneEvaluation(installed.Evaluation)}
}

func cloneRoute(route Route) Route {
	return Route{ID: route.ID, Path: clonePath(route.Path), Session: route.Session}
}

func clonePath(path Path) Path {
	result := Path{Segments: make([]Segment, len(path.Segments))}
	for i, segment := range path.Segments {
		result.Segments[i] = Segment{
			Type: segment.Type,
			ASNs: append([]ASN(nil), segment.ASNs...),
		}
	}
	return result
}

func cloneEvaluation(evaluation Evaluation) Evaluation {
	result := evaluation
	result.Path.ASNs = append([]ASN(nil), evaluation.Path.ASNs...)
	result.Path.Runs = append([]PrependRun(nil), evaluation.Path.Runs...)
	result.Hops = append([]Hop(nil), evaluation.Hops...)
	result.FirstIssue = cloneIssue(evaluation.FirstIssue)
	result.FirstInvalid = cloneIssue(evaluation.FirstInvalid)
	result.FirstDataGap = cloneIssue(evaluation.FirstDataGap)
	return result
}

func cloneIssue(issue *Issue) *Issue {
	if issue == nil {
		return nil
	}
	copy := *issue
	return &copy
}

func issueReason(issue *Issue) ReasonCode {
	if issue == nil {
		return ReasonNone
	}
	return issue.Reason
}
