package netsec

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ResolverConfig configures a source-pinned resolver.
//
// Cache may be nil, in which case a source-partitioned MemoryCache is created.
// SignatureMode's zero value requires signatures. Multiple RepositoryBindings
// with the same SourceID are ordered mirrors within that one source.
type ResolverConfig struct {
	Policy        *Policy
	Repositories  []RepositoryBinding
	Cache         Cache
	Verifier      SignatureVerifier
	SignatureMode SignatureMode
	Auditor       Auditor
}

// Resolution is a successful, authenticated module resolution.
type Resolution struct {
	ModulePath   string       `json:"module_path"`
	Version      string       `json:"version"`
	Source       Source       `json:"source"`
	Namespace    string       `json:"namespace"`
	Artifact     Artifact     `json:"artifact"`
	FromCache    bool         `json:"from_cache"`
	FallbackUsed bool         `json:"fallback_used"`
	Events       []AuditEvent `json:"events"`
}

// Resolver pins every module lookup to one namespace-declared source, verifies
// the response, and caches it under the source ID. It is safe for concurrent
// use when its repositories, cache, verifier, and auditor are safe as well.
type Resolver struct {
	policy        *Policy
	repositories  map[string][]Repository
	cache         Cache
	verifier      SignatureVerifier
	signatureMode SignatureMode
	auditor       Auditor
	now           func() time.Time
}

// NewResolver validates the source/repository mapping. Every source used by a
// namespace must have at least one repository. When signatures are required,
// a verifier must be supplied.
func NewResolver(cfg ResolverConfig) (*Resolver, error) {
	if cfg.Policy == nil {
		return nil, fmt.Errorf("%w: policy is nil", ErrInvalidResolverConfig)
	}
	switch cfg.SignatureMode {
	case SignaturesRequired, SignaturesIfPresent, SignaturesIgnored:
	default:
		return nil, fmt.Errorf("%w: unknown signature mode %d", ErrInvalidResolverConfig, cfg.SignatureMode)
	}
	if cfg.SignatureMode == SignaturesRequired && cfg.Verifier == nil {
		return nil, fmt.Errorf("%w: signatures are required but no verifier is configured", ErrInvalidResolverConfig)
	}
	if len(cfg.Repositories) == 0 {
		return nil, fmt.Errorf("%w: at least one repository is required", ErrInvalidResolverConfig)
	}

	repositories := make(map[string][]Repository, len(cfg.Policy.sources))
	for i, binding := range cfg.Repositories {
		sourceID := strings.TrimSpace(binding.SourceID)
		if sourceID == "" {
			return nil, fmt.Errorf("%w: repository binding %d has an empty source ID", ErrInvalidResolverConfig, i)
		}
		if _, ok := cfg.Policy.Source(sourceID); !ok {
			return nil, fmt.Errorf("%w: repository binding %d refers to unknown source %q", ErrInvalidResolverConfig, i, sourceID)
		}
		if binding.Repository == nil {
			return nil, fmt.Errorf("%w: repository binding %d for source %q is nil", ErrInvalidResolverConfig, i, sourceID)
		}
		repositories[sourceID] = append(repositories[sourceID], binding.Repository)
	}
	for _, source := range cfg.Policy.sources {
		if cfg.Policy.sourceUsed(source.ID) && len(repositories[source.ID]) == 0 {
			return nil, fmt.Errorf("%w: pinned source %q has no repository", ErrInvalidResolverConfig, source.ID)
		}
	}

	cache := cfg.Cache
	if cache == nil {
		cache = NewMemoryCache()
	}

	return &Resolver{
		policy:        cfg.Policy,
		repositories:  repositories,
		cache:         cache,
		verifier:      cfg.Verifier,
		signatureMode: cfg.SignatureMode,
		auditor:       cfg.Auditor,
		now:           time.Now,
	}, nil
}

// Resolve resolves exactly request.Version from the source pinned to
// request.ModulePath. It never probes repositories belonging to another source.
//
// Repository transport failures may fall back to later mirrors of the same
// source, and those fallbacks are audited. Identity, version, checksum, or
// signature failures are terminal and never trigger fallback.
func (r *Resolver) Resolve(ctx context.Context, request Request) (*Resolution, error) {
	if err := request.validate(); err != nil {
		return nil, err
	}

	source, namespace, ok := r.policy.match(request.ModulePath)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoDeclaredSource, request.ModulePath)
	}

	events := make([]AuditEvent, 0, 8)
	r.record(ctx, &events, AuditEvent{
		Kind: AuditSourceSelected, ModulePath: request.ModulePath,
		Version: request.Version, SourceID: source.ID, Namespace: namespace,
	})

	key := CacheKey{
		SourceID: source.ID, ModulePath: request.ModulePath, Version: request.Version,
	}
	cached, hit, cacheErr := r.cache.Get(ctx, key)
	if cacheErr != nil {
		r.record(ctx, &events, AuditEvent{
			Kind: AuditDegraded, ModulePath: request.ModulePath, Version: request.Version,
			SourceID: source.ID, Namespace: namespace,
			Detail: fmt.Sprintf("cache read failed; continuing with pinned source: %v", cacheErr),
		})
		hit = false
	}
	if hit {
		r.record(ctx, &events, AuditEvent{
			Kind: AuditCacheHit, ModulePath: request.ModulePath,
			Version: request.Version, SourceID: source.ID, Namespace: namespace,
		})
		if err := r.verifyArtifact(ctx, source, namespace, request, cached, &events); err != nil {
			r.record(ctx, &events, AuditEvent{
				Kind: AuditRejected, ModulePath: request.ModulePath, Version: request.Version,
				SourceID: source.ID, Namespace: namespace, Detail: err.Error(),
			})
			return nil, fmt.Errorf("cache hit rejected: %w", err)
		}
		return &Resolution{
			ModulePath: request.ModulePath, Version: request.Version,
			Source: source, Namespace: namespace, Artifact: cached,
			FromCache: true, Events: events,
		}, nil
	}

	r.record(ctx, &events, AuditEvent{
		Kind: AuditCacheMiss, ModulePath: request.ModulePath,
		Version: request.Version, SourceID: source.ID, Namespace: namespace,
	})

	repositories := r.repositories[source.ID]
	fallbackUsed := false
	var lastErr error
	for index, repository := range repositories {
		r.record(ctx, &events, AuditEvent{
			Kind: AuditRepositoryFetch, ModulePath: request.ModulePath,
			Version: request.Version, SourceID: source.ID, Namespace: namespace,
			Detail: fmt.Sprintf("repository index %d", index),
		})
		artifact, err := repository.Fetch(ctx, source, request)
		if err != nil {
			lastErr = err
			r.record(ctx, &events, AuditEvent{
				Kind: AuditRepositoryFailed, ModulePath: request.ModulePath,
				Version: request.Version, SourceID: source.ID, Namespace: namespace,
				Detail: fmt.Sprintf("repository index %d: %v", index, err),
			})
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, fmt.Errorf("%w: %v", ErrSourceUnavailable, ctxErr)
			}
			if index+1 < len(repositories) {
				fallbackUsed = true
				r.record(ctx, &events, AuditEvent{
					Kind: AuditFallback, ModulePath: request.ModulePath,
					Version: request.Version, SourceID: source.ID, Namespace: namespace,
					Detail: fmt.Sprintf("same-source mirror %d failed; trying mirror %d: %v", index, index+1, err),
				})
				r.record(ctx, &events, AuditEvent{
					Kind: AuditDegraded, ModulePath: request.ModulePath,
					Version: request.Version, SourceID: source.ID, Namespace: namespace,
					Detail: "resolution degraded while using a fixed-order same-source mirror",
				})
				continue
			}
			r.record(ctx, &events, AuditEvent{
				Kind: AuditFallbackBlocked, ModulePath: request.ModulePath,
				Version: request.Version, SourceID: source.ID, Namespace: namespace,
				Detail: "all repositories for the pinned source failed; cross-source fallback is prohibited",
			})
			return nil, fmt.Errorf("%w: source %q: last error: %v", ErrSourceUnavailable, source.ID, lastErr)
		}

		if err := r.verifyArtifact(ctx, source, namespace, request, artifact, &events); err != nil {
			r.record(ctx, &events, AuditEvent{
				Kind: AuditRejected, ModulePath: request.ModulePath, Version: request.Version,
				SourceID: source.ID, Namespace: namespace, Detail: err.Error(),
			})
			return nil, fmt.Errorf("artifact from source %q rejected: %w", source.ID, err)
		}

		if putErr := r.cache.Put(ctx, key, artifact); putErr != nil {
			r.record(ctx, &events, AuditEvent{
				Kind: AuditCacheWriteFailed, ModulePath: request.ModulePath,
				Version: request.Version, SourceID: source.ID, Namespace: namespace,
				Detail: fmt.Sprintf("cache write failed; resolution remains valid: %v", putErr),
			})
			r.record(ctx, &events, AuditEvent{
				Kind: AuditDegraded, ModulePath: request.ModulePath,
				Version: request.Version, SourceID: source.ID, Namespace: namespace,
				Detail: "cache write failed but verified artifact is returned",
			})
		}
		r.record(ctx, &events, AuditEvent{
			Kind: AuditVerified, ModulePath: request.ModulePath,
			Version: request.Version, SourceID: source.ID, Namespace: namespace,
		})
		return &Resolution{
			ModulePath: request.ModulePath, Version: request.Version,
			Source: source, Namespace: namespace, Artifact: artifact,
			FallbackUsed: fallbackUsed, Events: events,
		}, nil
	}

	return nil, fmt.Errorf("%w: no repository was attempted for source %q", ErrSourceUnavailable, source.ID)
}

func (r *Resolver) record(ctx context.Context, events *[]AuditEvent, event AuditEvent) {
	if event.At.IsZero() {
		event.At = r.now().UTC()
	}
	*events = append(*events, event)
	if r.auditor != nil {
		r.auditor.Record(ctx, event)
	}
}
