package netsec

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Engine evaluates scoped searches over a Store.
type Engine struct {
	store           Store
	maxChainHops    int
	maxIncludeDepth int
	maxResolutions  int
	defaultPageSize int
	maxPageSize     int
}

// EngineOption configures traversal and paging limits.
type EngineOption func(*Engine)

// WithMaxChainHops limits the number of reference hops allowed in a chained
// search parameter.
func WithMaxChainHops(hops int) EngineOption {
	return func(engine *Engine) {
		if hops > 0 {
			engine.maxChainHops = hops
		}
	}
}

// WithMaxIncludeDepth limits iterative _include/_revinclude expansion.
func WithMaxIncludeDepth(depth int) EngineOption {
	return func(engine *Engine) {
		if depth > 0 {
			engine.maxIncludeDepth = depth
		}
	}
}

// WithMaxResolutions limits the total number of reference resolutions in one
// search. It prevents pathological fan-out from turning a search into a
// resource-exhaustion primitive.
func WithMaxResolutions(resolutions int) EngineOption {
	return func(engine *Engine) {
		if resolutions > 0 {
			engine.maxResolutions = resolutions
		}
	}
}

// WithPageSize configures the default and maximum page size.
func WithPageSize(defaultSize, maxSize int) EngineOption {
	return func(engine *Engine) {
		if defaultSize > 0 {
			engine.defaultPageSize = defaultSize
		}
		if maxSize > 0 {
			engine.maxPageSize = maxSize
		}
	}
}

// NewEngine returns a scoped search engine.
func NewEngine(store Store, options ...EngineOption) *Engine {
	engine := &Engine{
		store:           store,
		maxChainHops:    16,
		maxIncludeDepth: 8,
		maxResolutions:  10000,
		defaultPageSize: DefaultPageSize,
		maxPageSize:     MaxPageSize,
	}
	for _, option := range options {
		if option != nil {
			option(engine)
		}
	}
	return engine
}

// NewSearchEngine is an alias for NewEngine.
func NewSearchEngine(store Store, options ...EngineOption) *Engine {
	return NewEngine(store, options...)
}

// New is an alias for NewEngine.
func New(store Store, options ...EngineOption) *Engine {
	return NewEngine(store, options...)
}

// SearchResult contains one page of matches plus resources reached through
// _include/_revinclude. Total, pagination, and the page itself are derived from
// the same authorized match set.
type SearchResult struct {
	ResourceType string
	Total        int
	Offset       int
	Limit        int
	Returned     int
	HasMore      bool
	Resources    []*Resource
	Included     []*Resource
}

// Result is an alias for SearchResult.
type Result = SearchResult

// Searcher is an alias for Engine.
type Searcher = Engine

// All returns the page resources followed by included resources.
func (r *SearchResult) All() []*Resource {
	if r == nil {
		return nil
	}
	resources := make([]*Resource, 0, len(r.Resources)+len(r.Included))
	resources = append(resources, r.Resources...)
	resources = append(resources, r.Included...)
	return resources
}

// Search parses params and evaluates them under scope.
func (e *Engine) Search(ctx context.Context, scope Scope, resourceType string, params url.Values) (*SearchResult, error) {
	return e.search(ctx, scope, resourceType, params, false)
}

// SearchQuery parses a raw query string and evaluates it under scope.
func (e *Engine) SearchQuery(ctx context.Context, scope Scope, resourceType, rawQuery string) (*SearchResult, error) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidQuery, err)
	}
	return e.Search(ctx, scope, resourceType, values)
}

// SearchURL uses the last resource-type path segment and the URL query.
func (e *Engine) SearchURL(ctx context.Context, scope Scope, requestURL *url.URL) (*SearchResult, error) {
	if requestURL == nil {
		return nil, fmt.Errorf("%w: nil URL", ErrInvalidQuery)
	}
	resourceType := ""
	segments := strings.Split(requestURL.Path, "/")
	for i := len(segments) - 1; i >= 0; i-- {
		segment := segments[i]
		if segment == "" || segment == "_history" || isDigits(segment) {
			continue
		}
		if validResourceType(segment) {
			resourceType = segment
			break
		}
	}
	if resourceType == "" {
		return nil, fmt.Errorf("%w: URL has no resource type", ErrInvalidQuery)
	}
	return e.Search(ctx, scope, resourceType, requestURL.Query())
}

// Count returns the number of authorized matches using exactly the same
// filtering pipeline as Search. Pagination parameters do not change the count.
func (e *Engine) Count(ctx context.Context, scope Scope, resourceType string, params url.Values) (int, error) {
	countParams := cloneValues(params)
	countParams.Set("_summary", "count")
	result, err := e.search(ctx, scope, resourceType, countParams, true)
	if err != nil {
		return 0, err
	}
	return result.Total, nil
}

// SearchWithScope is a package-level convenience wrapper around Engine.
func SearchWithScope(ctx context.Context, store Store, scope Scope, resourceType string, params url.Values) (*SearchResult, error) {
	return NewEngine(store).Search(ctx, scope, resourceType, params)
}

// Search is an alias for SearchWithScope.
func Search(ctx context.Context, store Store, scope Scope, resourceType string, params url.Values) (*SearchResult, error) {
	return SearchWithScope(ctx, store, scope, resourceType, params)
}

func (e *Engine) search(ctx context.Context, scope Scope, resourceType string, params url.Values, forceCount bool) (*SearchResult, error) {
	if e == nil || e.store == nil {
		return nil, fmt.Errorf("%w: nil store", ErrInvalidScope)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	scopeSnapshot, err := scope.snapshot()
	if err != nil {
		return nil, err
	}
	query, err := ParseSearch(resourceType, params)
	if err != nil {
		return nil, err
	}
	if forceCount {
		query.CountOnly = true
	}
	for _, condition := range query.Conditions {
		if condition.ChainDepth() > e.maxChainHops {
			return nil, fmt.Errorf("%w: chain has %d hops, maximum is %d", ErrTooDeep, condition.ChainDepth(), e.maxChainHops)
		}
	}
	if !query.CountOnly {
		if !query.limitSet {
			query.Limit = e.defaultPageSize
		}
		if query.Limit > e.maxPageSize {
			query.Limit = e.maxPageSize
		}
	}

	state := &searchState{
		ctx:            ctx,
		engine:         e,
		scope:          scopeSnapshot,
		cache:          make(map[string]cacheEntry),
		resolutions:    0,
		maxResolutions: e.maxResolutions,
	}

	candidates, err := e.store.List(ctx, scopeSnapshot.tenantID, query.ResourceType)
	if err != nil {
		return nil, fmt.Errorf("netsec: list %s: %w", query.ResourceType, err)
	}
	candidates = uniqueResources(candidates)
	sortResources(candidates)

	matches := make([]*Resource, 0, len(candidates))
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !scopeSnapshot.authorized(candidate) {
			continue
		}
		ok, err := state.matches(candidate, query.Conditions)
		if err != nil {
			return nil, err
		}
		if ok {
			matches = append(matches, candidate)
		}
	}
	matches = uniqueResources(matches)
	sortResources(matches)

	result := &SearchResult{
		ResourceType: query.ResourceType,
		Total:        len(matches),
		Offset:       query.Offset,
		Limit:        query.Limit,
	}
	if query.CountOnly {
		return result, nil
	}

	start := query.Offset
	if start > len(matches) {
		start = len(matches)
	}
	end := start + query.Limit
	if end > len(matches) {
		end = len(matches)
	}
	result.Resources = append([]*Resource(nil), matches[start:end]...)
	result.Returned = len(result.Resources)
	result.HasMore = end < len(matches)

	included, err := state.resolveIncludes(query, result.Resources)
	if err != nil {
		return nil, err
	}
	result.Included = included
	return result, nil
}

type cacheEntry struct {
	resource *Resource
	missing  bool
}

type searchState struct {
	ctx            context.Context
	engine         *Engine
	scope          scopeState
	cache          map[string]cacheEntry
	resolutions    int
	maxResolutions int
}

func (s *searchState) spend() error {
	if err := s.ctx.Err(); err != nil {
		return err
	}
	s.resolutions++
	if s.maxResolutions > 0 && s.resolutions > s.maxResolutions {
		return fmt.Errorf("%w: more than %d reference resolutions", ErrBudgetExceeded, s.maxResolutions)
	}
	return nil
}

func (s *searchState) get(resourceType, resourceID string) (*Resource, error) {
	if err := s.spend(); err != nil {
		return nil, err
	}
	key := resourceType + "\x00" + resourceID
	if cached, ok := s.cache[key]; ok {
		if cached.missing {
			return nil, fmt.Errorf("%w: %s/%s", ErrNotFound, resourceType, resourceID)
		}
		return cached.resource, nil
	}
	resource, err := s.engine.store.Get(s.ctx, s.scope.tenantID, resourceType, resourceID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			s.cache[key] = cacheEntry{missing: true}
		}
		return nil, err
	}
	if resource == nil {
		s.cache[key] = cacheEntry{missing: true}
		return nil, fmt.Errorf("%w: %s/%s", ErrNotFound, resourceType, resourceID)
	}
	s.cache[key] = cacheEntry{resource: resource}
	return resource, nil
}

func (s *searchState) matches(resource *Resource, conditions []Condition) (bool, error) {
	for _, condition := range conditions {
		ok, err := s.matchCondition(resource, condition)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func (s *searchState) matchCondition(resource *Resource, condition Condition) (bool, error) {
	if len(condition.Chain) == 0 {
		return matchValues(resource, condition), nil
	}

	frontier := []*Resource{resource}
	for _, hop := range condition.Chain {
		next := make(map[string]*Resource)
		for _, current := range frontier {
			for _, raw := range current.referenceValues(hop.Param) {
				ref, err := ParseReference(raw)
				if err != nil {
					// An unresolvable reference cannot widen a search.
					continue
				}
				if ref.Type == "" {
					ref.Type = hop.TargetType
				}
				if ref.Type != hop.TargetType {
					continue
				}
				target, err := s.get(ref.Type, ref.ID)
				if err != nil {
					if errors.Is(err, ErrNotFound) {
						continue
					}
					return false, err
				}
				// Authorization is enforced before a resource can participate
				// in the next hop. A denied hop terminates that path.
				if !s.scope.authorized(target) {
					continue
				}
				next[target.Key()] = target
			}
		}
		if len(next) == 0 {
			return false, nil
		}
		frontier = sortedMapValues(next)
	}

	for _, target := range frontier {
		if matchValues(target, condition) {
			return true, nil
		}
	}
	return false, nil
}

func (s *searchState) resolveIncludes(query *SearchQuery, page []*Resource) ([]*Resource, error) {
	known := make(map[string]struct{}, len(page))
	for _, resource := range page {
		known[resource.Key()] = struct{}{}
	}
	included := make(map[string]*Resource)

	add := func(resource *Resource) bool {
		if resource == nil {
			return false
		}
		key := resource.Key()
		if _, ok := known[key]; ok {
			return false
		}
		known[key] = struct{}{}
		included[key] = resource
		return true
	}

	for _, spec := range query.Includes {
		if spec.Iterate {
			continue
		}
		if err := s.expandForward(spec, page, add); err != nil {
			return nil, err
		}
	}
	for _, spec := range query.ReverseIncludes {
		if spec.Iterate {
			continue
		}
		if err := s.expandReverse(spec, page, add); err != nil {
			return nil, err
		}
	}

	frontier := append([]*Resource(nil), page...)
	for depth := 0; depth < s.engine.maxIncludeDepth && len(frontier) > 0; depth++ {
		var next []*Resource
		collect := func(resource *Resource) bool {
			if resource == nil {
				return false
			}
			key := resource.Key()
			if _, ok := known[key]; ok {
				return false
			}
			known[key] = struct{}{}
			included[key] = resource
			next = append(next, resource)
			return true
		}
		for _, spec := range query.Includes {
			if !spec.Iterate {
				continue
			}
			if err := s.expandForward(spec, frontier, collect); err != nil {
				return nil, err
			}
		}
		for _, spec := range query.ReverseIncludes {
			if !spec.Iterate {
				continue
			}
			if err := s.expandReverse(spec, frontier, collect); err != nil {
				return nil, err
			}
		}
		frontier = uniqueResources(next)
		sortResources(frontier)
	}

	resources := make([]*Resource, 0, len(included))
	for _, resource := range included {
		resources = append(resources, resource)
	}
	sortResources(resources)
	return resources, nil
}

func (s *searchState) expandForward(spec IncludeSpec, sources []*Resource, add func(*Resource) bool) error {
	for _, source := range sources {
		if !spec.allowsForward(source) {
			continue
		}
		for _, param := range includeParams(source, spec.Param) {
			for _, raw := range source.referenceValues(param) {
				ref, err := ParseReference(raw)
				if err != nil {
					continue
				}
				if ref.Type == "" {
					if len(spec.TargetTypes) != 1 || spec.TargetTypes[0] == "*" {
						continue
					}
					ref.Type = spec.TargetTypes[0]
				}
				if !spec.allowsForwardTarget(&Resource{Type: ref.Type}) {
					continue
				}
				target, err := s.get(ref.Type, ref.ID)
				if err != nil {
					if errors.Is(err, ErrNotFound) {
						continue
					}
					return err
				}
				// The same authorization predicate used for base resources is
				// applied to every included target.
				if !s.scope.authorized(target) {
					continue
				}
				if !spec.allowsForwardTarget(target) {
					continue
				}
				add(target)
			}
		}
	}
	return nil
}

func (s *searchState) expandReverse(spec IncludeSpec, targets []*Resource, add func(*Resource) bool) error {
	for _, target := range targets {
		if !spec.allowsReverseTarget(target) {
			continue
		}
		targetRef := Reference{Type: target.Type, ID: target.ID}
		var candidates []*Resource
		var err error
		if index, ok := s.engine.store.(ReferenceIndex); ok {
			if err = s.spend(); err != nil {
				return err
			}
			candidates, err = index.FindReferencing(s.ctx, s.scope.tenantID, spec.SourceType, targetRef)
		} else {
			if err = s.spend(); err != nil {
				return err
			}
			candidates, err = s.engine.store.List(s.ctx, s.scope.tenantID, spec.SourceType)
		}
		if err != nil {
			return fmt.Errorf("netsec: reverse include %s: %w", spec.SourceType, err)
		}
		for _, candidate := range candidates {
			if candidate.Type != spec.SourceType {
				continue
			}
			if spec.Param != "*" && !referencesParamTarget(candidate, spec.Param, targetRef) {
				continue
			}
			if spec.Param == "*" && !referencesTarget(candidate, targetRef) {
				continue
			}
			if !s.scope.authorized(candidate) {
				continue
			}
			add(candidate)
		}
	}
	return nil
}

func includeParams(resource *Resource, param string) []string {
	if param != "*" {
		return []string{param}
	}
	return resource.referenceParams()
}

func referencesParamTarget(resource *Resource, param string, target Reference) bool {
	for _, raw := range resource.referenceValues(param) {
		ref, err := ParseReference(raw)
		if err != nil {
			continue
		}
		if target.Type != "" && ref.Type != "" && ref.Type != target.Type {
			continue
		}
		if ref.ID == target.ID {
			return true
		}
	}
	return false
}

func matchValues(resource *Resource, condition Condition) bool {
	param := condition.Param
	values := condition.Values

	switch condition.Modifier {
	case "missing":
		want, err := strconv.ParseBool(strings.TrimSpace(strings.Join(values, "")))
		if err != nil {
			return false
		}
		return resource.hasParam(param) != want
	case "not":
		for _, want := range values {
			if matchAnyStored(resource, param, want) {
				return false
			}
		}
		return true
	}

	for _, want := range values {
		if matchAnyStored(resource, param, want) {
			return true
		}
	}
	return false
}

func matchAnyStored(resource *Resource, param, want string) bool {
	storedValues := resource.values(param)
	for _, stored := range storedValues {
		if storedMatches(stored, want) {
			return true
		}
	}
	return false
}

func storedMatches(stored, want string) bool {
	stored = strings.TrimSpace(stored)
	want = strings.TrimSpace(want)
	if stored == want {
		return true
	}

	if code, ok := tokenCode(want); ok {
		if stored == code {
			return true
		}
		if storedCode, ok := tokenCode(stored); ok && storedCode == code {
			return true
		}
	}

	storedRef, storedErr := ParseReference(stored)
	wantRef, wantErr := ParseReference(want)
	if storedErr == nil && wantErr == nil {
		if wantRef.Type != "" && storedRef.Type != "" && wantRef.Type != storedRef.Type {
			return false
		}
		if wantRef.ID != "" && storedRef.ID == wantRef.ID {
			return true
		}
	}
	return false
}

func tokenCode(value string) (string, bool) {
	if !strings.Contains(value, "|") {
		return "", false
	}
	parts := strings.SplitN(value, "|", 2)
	if parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func uniqueResources(resources []*Resource) []*Resource {
	seen := make(map[string]struct{}, len(resources))
	result := make([]*Resource, 0, len(resources))
	for _, resource := range resources {
		if resource == nil {
			continue
		}
		key := resource.Key()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, resource)
	}
	return result
}

func sortedMapValues(values map[string]*Resource) []*Resource {
	resources := make([]*Resource, 0, len(values))
	for _, resource := range values {
		resources = append(resources, resource)
	}
	sortResources(resources)
	return resources
}

func cloneValues(values url.Values) url.Values {
	cloned := make(url.Values, len(values))
	for key, items := range values {
		cloned[key] = append([]string(nil), items...)
	}
	return cloned
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
