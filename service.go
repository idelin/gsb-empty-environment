package netsec

import (
	"context"
	"fmt"
	"sync"
)

// Upstream supplies an already-DNSSEC-validated denial response for one view.
// It is called only after a cache miss, and never for another view.
type Upstream interface {
	Respond(context.Context, Query) (*Response, error)
}

// UpstreamFunc adapts a function to Upstream.
type UpstreamFunc func(context.Context, Query) (*Response, error)

// Respond implements Upstream.
func (f UpstreamFunc) Respond(ctx context.Context, q Query) (*Response, error) {
	return f(ctx, q)
}

// Service wires per-view upstreams to a shared aggressive negative cache. The
// shared cache is safe because every key includes the tenant view.
type Service struct {
	cache     *Cache
	mu        sync.RWMutex
	upstreams map[TenantView]Upstream
}

// NewService creates a service around an existing cache.
func NewService(cache *Cache) *Service {
	if cache == nil {
		cache = NewCache(nil)
	}
	return &Service{
		cache:     cache,
		upstreams: make(map[TenantView]Upstream),
	}
}

// RegisterUpstream installs the sole upstream for a tenant view.
func (s *Service) RegisterUpstream(view TenantView, upstream Upstream) error {
	if view == "" {
		return fmt.Errorf("%w: empty view", ErrViewMismatch)
	}
	if upstream == nil {
		return fmt.Errorf("%w: nil upstream", ErrNoUpstream)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.upstreams[view] = upstream
	return nil
}

// UnregisterUpstream removes a view's upstream.
func (s *Service) UnregisterUpstream(view TenantView) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.upstreams, view)
}

// Cache exposes the underlying cache for policy management.
func (s *Service) Cache() *Cache { return s.cache }

// Query answers from the cache first. On a miss it consults only the upstream
// registered for q.View, refuses a proof for a different question/view, then
// validates and caches it.
func (s *Service) Query(ctx context.Context, q Query) (Answer, error) {
	if err := ctx.Err(); err != nil {
		return Answer{}, err
	}
	q, err := normalizeQuery(q)
	if err != nil {
		return Answer{}, err
	}
	if answer, ok := s.cache.Lookup(q); ok {
		return answer, nil
	}

	s.mu.RLock()
	upstream, ok := s.upstreams[q.View]
	s.mu.RUnlock()
	if !ok {
		return Answer{}, fmt.Errorf("%w: %s", ErrNoUpstream, q.View)
	}
	resp, err := upstream.Respond(ctx, q)
	if err != nil {
		return Answer{}, err
	}
	if resp == nil {
		return Answer{}, ErrNoUpstreamResponse
	}
	if resp.Attestation.View != q.View {
		return Answer{}, fmt.Errorf("%w: upstream returned view %s for %s", ErrViewMismatch, resp.Attestation.View, q.View)
	}
	if !resp.Question.Name.Equal(q.Name) || resp.Question.Type != q.Type {
		return Answer{}, fmt.Errorf("%w: upstream proof is for %s/%d", ErrQuestionMismatch, resp.Question.Name, resp.Question.Type)
	}
	if !q.Apex.IsZero() && !resp.Attestation.Apex.Equal(q.Apex) {
		return Answer{}, fmt.Errorf("%w: upstream proof apex %s, requested %s", ErrQuestionMismatch, resp.Attestation.Apex, q.Apex)
	}
	if q.Parameters != nil && !resp.Attestation.Parameters.Equal(*q.Parameters) {
		return Answer{}, fmt.Errorf("%w: upstream proof parameter set differs", ErrQuestionMismatch)
	}
	questionClass := resp.Question.Class
	if questionClass == 0 {
		questionClass = resp.Attestation.Class
	}
	if questionClass == 0 {
		questionClass = q.Class
	}
	if questionClass != q.Class {
		return Answer{}, fmt.Errorf("%w: upstream proof class %d", ErrQuestionMismatch, questionClass)
	}
	answer, err := s.cache.Ingest(*resp)
	if err != nil {
		return Answer{}, err
	}
	answer.CacheHit = false
	return answer, nil
}
