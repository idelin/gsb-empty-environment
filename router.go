package netsec

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Router routes jobs to executors. It is safe for concurrent use.
type Router struct {
	mu sync.RWMutex

	domains          map[TrustLevel]TrustDomain
	allowedDomains   map[TrustDomain]struct{}
	privilegedLabels map[string]struct{}
	privilegedCaps   map[Capability]struct{}
	repositories     map[string]Repository
	executors        map[string]Executor
	retiredDomains   map[string]TrustDomain
}

var builtinDomains = map[TrustLevel]TrustDomain{
	TrustUntrusted:  DomainUntrusted,
	TrustTrusted:    DomainTrusted,
	TrustPrivileged: DomainProduction,
}

// NewRouter validates config and builds an initially empty executor inventory.
func NewRouter(config Config) (*Router, error) {
	domains, err := normalizeDomains(config.Domains)
	if err != nil {
		return nil, err
	}
	allowed := make(map[TrustDomain]struct{}, len(domains))
	for _, domain := range domains {
		allowed[domain] = struct{}{}
	}

	r := &Router{
		domains:          domains,
		allowedDomains:   allowed,
		privilegedLabels: make(map[string]struct{}),
		privilegedCaps:   make(map[Capability]struct{}),
		repositories:     make(map[string]Repository),
		executors:        make(map[string]Executor),
		retiredDomains:   make(map[string]TrustDomain),
	}

	for _, label := range config.PrivilegedLabels {
		if err := validateToken("privileged label", label); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
		}
		r.privilegedLabels[label] = struct{}{}
	}
	for _, capability := range config.PrivilegedCapabilities {
		if err := validateToken("privileged capability", string(capability)); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
		}
		r.privilegedCaps[capability] = struct{}{}
	}
	for _, repository := range config.Repositories {
		if err := r.registerRepository(repository); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func normalizeDomains(configured map[TrustLevel]TrustDomain) (map[TrustLevel]TrustDomain, error) {
	if configured == nil {
		return map[TrustLevel]TrustDomain{
			TrustUntrusted:  builtinDomains[TrustUntrusted],
			TrustTrusted:    builtinDomains[TrustTrusted],
			TrustPrivileged: builtinDomains[TrustPrivileged],
		}, nil
	}
	domains := make(map[TrustLevel]TrustDomain, 3)
	seen := make(map[TrustDomain]struct{}, 3)
	for _, trust := range []TrustLevel{TrustUntrusted, TrustTrusted, TrustPrivileged} {
		domain, ok := configured[trust]
		if !ok || domain == "" {
			return nil, fmt.Errorf("%w: missing domain for trust level %s", ErrInvalidConfig, trust)
		}
		if err := validateDomainToken(domain); err != nil {
			return nil, err
		}
		if _, duplicate := seen[domain]; duplicate {
			return nil, fmt.Errorf("%w: multiple trust levels share domain %q", ErrInvalidConfig, domain)
		}
		seen[domain] = struct{}{}
		domains[trust] = domain
	}
	for trust := range configured {
		if !trust.valid() {
			return nil, fmt.Errorf("%w: invalid trust level %d", ErrInvalidConfig, trust)
		}
	}
	return domains, nil
}

func validateDomainToken(domain TrustDomain) error {
	if err := validateToken("domain", string(domain)); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	for _, r := range domain {
		if r == '/' || r == ':' {
			return fmt.Errorf("%w: domain %q contains a separator", ErrInvalidConfig, domain)
		}
	}
	return nil
}

func (r *Router) registerRepository(repository Repository) error {
	if err := validateRepository(repository); err != nil {
		return err
	}
	if _, exists := r.repositories[repository.ID]; exists {
		return fmt.Errorf("%w: %q", ErrRepositoryExists, repository.ID)
	}
	r.repositories[repository.ID] = cloneRepository(repository)
	return nil
}

// RegisterRepository adds a repository policy. Duplicate IDs are rejected so
// that trust changes must be explicit.
func (r *Router) RegisterRepository(repository Repository) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.registerRepository(repository)
}

// UpdateRepository replaces a repository policy. Callers use this to raise or
// lower repository trust and change protected branch patterns atomically.
func (r *Router) UpdateRepository(repository Repository) error {
	if err := validateRepository(repository); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.repositories[repository.ID]; !exists {
		return fmt.Errorf("%w: %q", ErrRepositoryNotFound, repository.ID)
	}
	r.repositories[repository.ID] = cloneRepository(repository)
	return nil
}

// SetRepositoryTrust lowers or raises a repository's trust without changing
// its protected branch policy.
func (r *Router) SetRepositoryTrust(repositoryID string, trust TrustLevel) error {
	if !trust.valid() {
		return fmt.Errorf("%w: invalid trust level %d", ErrInvalidRepository, trust)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	repository, exists := r.repositories[repositoryID]
	if !exists {
		return fmt.Errorf("%w: %q", ErrRepositoryNotFound, repositoryID)
	}
	repository.Trust = trust
	r.repositories[repositoryID] = repository
	return nil
}

// SetProtectedBranches replaces the protected branch patterns for a
// repository.
func (r *Router) SetProtectedBranches(repositoryID string, patterns []string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	repository, exists := r.repositories[repositoryID]
	if !exists {
		return fmt.Errorf("%w: %q", ErrRepositoryNotFound, repositoryID)
	}
	normalized, err := normalizePatterns(patterns)
	if err != nil {
		return err
	}
	repository.ProtectedBranches = normalized
	r.repositories[repositoryID] = repository
	return nil
}

// Repository returns a copy of the repository policy.
func (r *Router) Repository(id string) (Repository, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	repository, ok := r.repositories[id]
	if !ok {
		return Repository{}, false
	}
	return cloneRepository(repository), true
}

func validateRepository(repository Repository) error {
	if err := validateName("repository", repository.ID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidRepository, err)
	}
	if !repository.Trust.valid() {
		return fmt.Errorf("%w: invalid trust level %d", ErrInvalidRepository, repository.Trust)
	}
	_, err := normalizePatterns(repository.ProtectedBranches)
	return err
}

func normalizePatterns(patterns []string) ([]string, error) {
	normalized := make([]string, 0, len(patterns))
	seen := make(map[string]struct{}, len(patterns))
	for _, pattern := range patterns {
		value, err := validatePattern(pattern)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	return normalized, nil
}

func cloneRepository(repository Repository) Repository {
	repository.ProtectedBranches = append([]string(nil), repository.ProtectedBranches...)
	return repository
}

// RegisterExecutor adds an executor. Registration is rejected if the executor
// advertises a privileged label or capability outside the privileged domain.
func (r *Router) RegisterExecutor(executor Executor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.validateExecutor(executor); err != nil {
		return err
	}
	if retiredDomain, retired := r.retiredDomains[executor.ID]; retired && retiredDomain != executor.Domain {
		return fmt.Errorf("%w: executor %q was retired from %q, attempted %q", ErrDomainChange, executor.ID, retiredDomain, executor.Domain)
	}
	if _, exists := r.executors[executor.ID]; exists {
		return fmt.Errorf("%w: %q", ErrExecutorExists, executor.ID)
	}
	r.executors[executor.ID] = cloneExecutor(executor)
	delete(r.retiredDomains, executor.ID)
	return nil
}

// UpdateExecutor changes an executor's labels and capabilities. The executor's
// trust domain is immutable: moving an ID between domains would allow one
// registration to borrow another domain's identity.
func (r *Router) UpdateExecutor(executor Executor) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.validateExecutor(executor); err != nil {
		return err
	}
	existing, exists := r.executors[executor.ID]
	if !exists {
		return fmt.Errorf("%w: %q", ErrExecutorNotFound, executor.ID)
	}
	if existing.Domain != executor.Domain {
		return fmt.Errorf("%w: executor %q is in %q, attempted %q", ErrDomainChange, executor.ID, existing.Domain, executor.Domain)
	}
	r.executors[executor.ID] = cloneExecutor(executor)
	return nil
}

// RemoveExecutor unregisters an executor.
func (r *Router) RemoveExecutor(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	executor, exists := r.executors[id]
	if !exists {
		return fmt.Errorf("%w: %q", ErrExecutorNotFound, id)
	}
	delete(r.executors, id)
	r.retiredDomains[id] = executor.Domain
	return nil
}

// Executor returns a copy of an executor registration.
func (r *Router) Executor(id string) (Executor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	executor, ok := r.executors[id]
	if !ok {
		return Executor{}, false
	}
	return cloneExecutor(executor), true
}

func (r *Router) validateExecutor(executor Executor) error {
	if err := validateName("executor ID", executor.ID); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidExecutor, err)
	}
	if err := validateDomain(executor.Domain, r.allowedDomains); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidExecutor, err)
	}
	if err := validateLabels(executor.Labels); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidExecutor, err)
	}
	if err := validateCapabilities(executor.Capabilities); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidExecutor, err)
	}

	privilegedDomain := r.domains[TrustPrivileged]
	if executor.Domain != privilegedDomain {
		for _, label := range executor.Labels {
			if _, privileged := r.privilegedLabels[label]; privileged {
				return fmt.Errorf("%w: privileged label %q requires domain %q", ErrInvalidExecutor, label, privilegedDomain)
			}
		}
		for _, capability := range executor.Capabilities {
			if _, privileged := r.privilegedCaps[capability]; privileged {
				return fmt.Errorf("%w: privileged capability %q requires domain %q", ErrInvalidExecutor, capability, privilegedDomain)
			}
		}
	}
	return nil
}

func cloneExecutor(executor Executor) Executor {
	executor.Labels = append([]string(nil), executor.Labels...)
	executor.Capabilities = append([]Capability(nil), executor.Capabilities...)
	return executor
}

// Resolve computes the trust decision for a job without selecting an executor.
// It is useful for audit logging and for testing trust changes.
func (r *Router) Resolve(job Job) (Decision, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.resolve(job)
}

func (r *Router) resolve(job Job) (Decision, error) {
	if err := validateJob(job); err != nil {
		return Decision{}, err
	}
	branch, isBranch, err := resolveRef(job)
	if err != nil {
		return Decision{}, err
	}

	repository, known := r.repositories[job.Repository]
	effectiveTrust := TrustUntrusted
	switch job.Source {
	case SourceRepository, SourceTrustedAutomation:
		if known {
			effectiveTrust = repository.Trust
		}
	case SourceFork, SourceUnknown, "":
		effectiveTrust = TrustUntrusted
	default:
		return Decision{}, fmt.Errorf("%w: %q", ErrInvalidSource, job.Source)
	}

	protected := false
	if isBranch && known {
		for _, pattern := range repository.ProtectedBranches {
			if branchMatches(pattern, branch) {
				protected = true
				break
			}
		}
	}

	// An unprotected branch of a privileged repository is still trusted
	// code, but it must not be placed on a production executor.
	if effectiveTrust == TrustPrivileged && !protected {
		effectiveTrust = TrustTrusted
	}

	decision := Decision{
		Repository:      job.Repository,
		RepositoryKnown: known,
		Source:          job.Source,
		Ref:             job.Ref,
		Branch:          branch,
		ProtectedBranch: protected,
		Trust:           effectiveTrust,
		Domain:          r.domains[effectiveTrust],
	}
	if err := r.authorize(job, decision); err != nil {
		return Decision{}, err
	}
	return decision, nil
}

func validateJob(job Job) error {
	if err := validateName("job ID", job.ID); err != nil {
		return err
	}
	if err := validateName("repository", job.Repository); err != nil {
		return err
	}
	if err := validateLabels(job.Labels); err != nil {
		return err
	}
	if err := validateCapabilities(job.RequiredCapabilities); err != nil {
		return err
	}
	if job.Queue != "" {
		if err := validateName("queue", job.Queue); err != nil {
			return err
		}
	}
	if job.CacheKey != "" {
		if err := validateName("cache key", job.CacheKey); err != nil {
			return err
		}
	}
	if job.ArtifactKey != "" {
		if err := validateName("artifact key", job.ArtifactKey); err != nil {
			return err
		}
	}
	return nil
}

func (r *Router) authorize(job Job, decision Decision) error {
	privileged := decision.Trust == TrustPrivileged && decision.ProtectedBranch
	for _, label := range job.Labels {
		if _, sensitive := r.privilegedLabels[label]; sensitive && !privileged {
			return fmt.Errorf("%w: label %q requires a protected branch in a privileged repository", ErrPrivilegedLabel, label)
		}
	}
	for _, capability := range job.RequiredCapabilities {
		if _, sensitive := r.privilegedCaps[capability]; sensitive && !privileged {
			return fmt.Errorf("%w: capability %q requires a protected branch in a privileged repository", ErrPrivilegedCapability, capability)
		}
	}
	return nil
}

// Route resolves the job's trust and selects the lexicographically first
// eligible executor in exactly that trust domain.
func (r *Router) Route(job Job) (Placement, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.route(job)
}

// RouteContext is the context-aware form of Route for scheduler integration.
func (r *Router) RouteContext(ctx context.Context, job Job) (Placement, error) {
	if ctx == nil {
		return r.Route(job)
	}
	if err := ctx.Err(); err != nil {
		return Placement{}, err
	}
	return r.Route(job)
}

func (r *Router) route(job Job) (Placement, error) {
	decision, err := r.resolve(job)
	if err != nil {
		return Placement{}, err
	}

	ids := make([]string, 0, len(r.executors))
	for id := range r.executors {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		executor := r.executors[id]
		if executor.Domain != decision.Domain {
			continue
		}
		if !containsAll(executor.Labels, job.Labels) {
			continue
		}
		if !containsAllCaps(executor.Capabilities, job.RequiredCapabilities) {
			continue
		}
		return r.placement(job, decision, executor), nil
	}

	return Placement{}, fmt.Errorf(
		"%w: domain=%q labels=%v capabilities=%v",
		ErrNoExecutor, decision.Domain, job.Labels, job.RequiredCapabilities,
	)
}

func (r *Router) placement(job Job, decision Decision, executor Executor) Placement {
	cacheKey := job.CacheKey
	if cacheKey == "" {
		cacheKey = job.ID
	}
	artifactKey := job.ArtifactKey
	if artifactKey == "" {
		artifactKey = job.ID
	}
	executor = cloneExecutor(executor)
	return Placement{
		ExecutorID:        executor.ID,
		Executor:          executor,
		Domain:            decision.Domain,
		Trust:             decision.Trust,
		ProtectedBranch:   decision.ProtectedBranch,
		Queue:             QueueName(decision.Domain, job.Queue),
		CacheNamespace:    CacheNamespace(decision.Domain, job.Repository, cacheKey),
		ArtifactNamespace: ArtifactNamespace(decision.Domain, job.Repository, artifactKey),
	}
}
