package netsec

import (
	"fmt"
	"sync"
)

type tenantState struct {
	record     TenantRecord
	resources  map[string]struct{}
	generation uint64
}

// Registry holds authoritative tenant registrations and current resource
// ownership. There is deliberately no API to mutate a registered record
// without advancing its generation.
type Registry struct {
	mu             sync.RWMutex
	tenants        map[string]*tenantState
	owners         map[string]string
	lastGeneration map[string]uint64
}

// NewRegistry returns an empty tenant registry.
func NewRegistry() *Registry {
	return &Registry{
		tenants:        make(map[string]*tenantState),
		owners:         make(map[string]string),
		lastGeneration: make(map[string]uint64),
	}
}

// RegisterTenant creates a new tenant registration. Registration claims only
// currently unowned resources; it never steals a resource from another tenant.
func (r *Registry) RegisterTenant(rec TenantRecord) error {
	normalized, resources, err := normalizeTenant(rec)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.initLocked()
	if _, exists := r.tenants[normalized.TenantID]; exists {
		return fmt.Errorf("%w: %s", ErrTenantExists, normalized.TenantID)
	}
	generation, err := r.nextGenerationLocked(normalized.TenantID)
	if err != nil {
		return err
	}
	r.tenants[normalized.TenantID] = &tenantState{
		record:     normalized,
		resources:  resources,
		generation: generation,
	}
	for resource := range resources {
		if _, owned := r.owners[resource]; !owned {
			r.owners[resource] = normalized.TenantID
		}
	}
	return nil
}

// UpdateTenant replaces a tenant registration and advances its generation,
// even when the supplied values are unchanged. Any in-flight result from the
// previous generation is therefore fenced.
func (r *Registry) UpdateTenant(rec TenantRecord) error {
	normalized, resources, err := normalizeTenant(rec)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	state, exists := r.tenants[normalized.TenantID]
	if !exists {
		return fmt.Errorf("%w: %s", ErrTenantNotFound, normalized.TenantID)
	}
	generation, err := r.nextGenerationLocked(normalized.TenantID)
	if err != nil {
		return err
	}
	state.record = normalized
	state.resources = resources
	state.generation = generation
	for resource := range resources {
		if _, owned := r.owners[resource]; !owned {
			r.owners[resource] = normalized.TenantID
		}
	}
	return nil
}

// UpsertTenant registers or updates a tenant. Updating advances the generation.
func (r *Registry) UpsertTenant(rec TenantRecord) error {
	normalized, resources, err := normalizeTenant(rec)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.initLocked()
	if state, exists := r.tenants[normalized.TenantID]; exists {
		generation, err := r.nextGenerationLocked(normalized.TenantID)
		if err != nil {
			return err
		}
		state.record = normalized
		state.resources = resources
		state.generation = generation
	} else {
		generation, err := r.nextGenerationLocked(normalized.TenantID)
		if err != nil {
			return err
		}
		r.tenants[normalized.TenantID] = &tenantState{
			record:     normalized,
			resources:  resources,
			generation: generation,
		}
	}
	for resource := range resources {
		if _, owned := r.owners[resource]; !owned {
			r.owners[resource] = normalized.TenantID
		}
	}
	return nil
}

func (r *Registry) initLocked() {
	if r.tenants == nil {
		r.tenants = make(map[string]*tenantState)
	}
	if r.owners == nil {
		r.owners = make(map[string]string)
	}
	if r.lastGeneration == nil {
		r.lastGeneration = make(map[string]uint64)
	}
}

// nextGenerationLocked never reuses a generation for a tenant ID, even after
// the tenant was removed. A removed-then-recreated tenant therefore cannot
// resurrect an old in-flight decision.
func (r *Registry) nextGenerationLocked(tenantID string) (uint64, error) {
	last := r.lastGeneration[tenantID]
	if last == ^uint64(0) {
		return 0, fmt.Errorf("%w: generation exhausted", ErrInvalidTenantRecord)
	}
	last++
	r.lastGeneration[tenantID] = last
	return last, nil
}

// RemoveTenant removes a registration and releases resources that it still
// owns. The tenant's generation disappears, so no in-flight result for it can
// pass the broker's final generation check.
func (r *Registry) RemoveTenant(tenantID string) error {
	if !validText(tenantID, maxTenantIDBytes) {
		return fmt.Errorf("%w: invalid tenant ID", ErrInvalidTenantRecord)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tenants[tenantID]; !exists {
		return fmt.Errorf("%w: %s", ErrTenantNotFound, tenantID)
	}
	delete(r.tenants, tenantID)
	for resource, owner := range r.owners {
		if owner == tenantID {
			delete(r.owners, resource)
		}
	}
	return nil
}

// SetResourceOwner assigns the authoritative owner of a source resource. The
// owner must be a registered tenant. A change advances the generations of both
// the previous and the new owner so old in-flight results cannot be published.
func (r *Registry) SetResourceOwner(resource, tenantID string) error {
	if !validText(resource, maxResourceBytes) {
		return fmt.Errorf("%w: invalid source resource", ErrInvalidTenantRecord)
	}
	if !validText(tenantID, maxTenantIDBytes) {
		return fmt.Errorf("%w: invalid tenant ID", ErrInvalidTenantRecord)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	newState, exists := r.tenants[tenantID]
	if !exists {
		return fmt.Errorf("%w: %s", ErrTenantNotFound, tenantID)
	}
	oldOwner, owned := r.owners[resource]
	if owned && oldOwner == tenantID {
		return nil
	}
	if owned {
		if oldState, ok := r.tenants[oldOwner]; ok {
			generation, err := r.nextGenerationLocked(oldOwner)
			if err != nil {
				return err
			}
			oldState.generation = generation
		}
	}
	generation, err := r.nextGenerationLocked(tenantID)
	if err != nil {
		return err
	}
	newState.generation = generation
	r.owners[resource] = tenantID
	return nil
}

// TransferResource moves ownership only if the resource is currently owned by
// the expected tenant.
func (r *Registry) TransferResource(resource, fromTenantID, toTenantID string) error {
	if !validText(resource, maxResourceBytes) {
		return fmt.Errorf("%w: invalid source resource", ErrInvalidTenantRecord)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	owner, owned := r.owners[resource]
	if !owned || owner != fromTenantID {
		return fmt.Errorf("%w: %s", ErrResourceOwnerMismatch, resource)
	}
	newState, exists := r.tenants[toTenantID]
	if !exists {
		return fmt.Errorf("%w: %s", ErrTenantNotFound, toTenantID)
	}
	oldState := r.tenants[fromTenantID]
	if oldState != nil {
		generation, err := r.nextGenerationLocked(fromTenantID)
		if err != nil {
			return err
		}
		oldState.generation = generation
	}
	if newState != oldState {
		generation, err := r.nextGenerationLocked(toTenantID)
		if err != nil {
			return err
		}
		newState.generation = generation
	}
	r.owners[resource] = toTenantID
	return nil
}

// ResourceOwner returns the current authoritative owner of a resource.
func (r *Registry) ResourceOwner(resource string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	owner, ok := r.owners[resource]
	return owner, ok
}

// Generation returns the current configuration generation for a tenant.
func (r *Registry) Generation(tenantID string) (uint64, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, ok := r.tenants[tenantID]
	if !ok {
		return 0, false
	}
	return state.generation, true
}

// TenantSnapshot is an immutable view of one tenant registration at a
// particular generation.
type TenantSnapshot struct {
	Record     TenantRecord
	Generation uint64
	resources  map[string]struct{}
}

// AllowsResource reports whether the resource was in the tenant's registered
// allowed set at snapshot time.
func (s TenantSnapshot) AllowsResource(resource string) bool {
	_, ok := s.resources[resource]
	return ok
}

// Snapshot returns a defensive copy of a tenant registration.
func (r *Registry) Snapshot(tenantID string) (TenantSnapshot, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	state, ok := r.tenants[tenantID]
	if !ok {
		return TenantSnapshot{}, false
	}
	record := cloneRecord(state.record)
	resources := make(map[string]struct{}, len(state.resources))
	for resource := range state.resources {
		resources[resource] = struct{}{}
	}
	return TenantSnapshot{
		Record:     record,
		Generation: state.generation,
		resources:  resources,
	}, true
}

func cloneRecord(rec TenantRecord) TenantRecord {
	out := rec
	if rec.SourceResources != nil {
		out.SourceResources = append([]string(nil), rec.SourceResources...)
	}
	return out
}

func normalizeTenant(rec TenantRecord) (TenantRecord, map[string]struct{}, error) {
	if !validText(rec.TenantID, maxTenantIDBytes) {
		return TenantRecord{}, nil, fmt.Errorf("%w: invalid tenant ID", ErrInvalidTenantRecord)
	}
	if !validText(rec.RoleID, maxRoleIDBytes) {
		return TenantRecord{}, nil, fmt.Errorf("%w: invalid role ID", ErrInvalidTenantRecord)
	}
	if !validText(rec.ExternalID, maxExternalIDBytes) {
		return TenantRecord{}, nil, fmt.Errorf("%w: invalid external ID", ErrInvalidTenantRecord)
	}
	if rec.MaxSessionSeconds <= 0 || rec.MaxSessionSeconds > maxSessionSecondsLimit {
		return TenantRecord{}, nil, fmt.Errorf("%w: invalid maximum session duration", ErrInvalidTenantRecord)
	}
	if !validText(rec.PolicyDigest, maxPolicyDigestBytes) {
		return TenantRecord{}, nil, fmt.Errorf("%w: invalid policy digest", ErrInvalidTenantRecord)
	}
	if rec.Audience == "" {
		rec.Audience = rec.TenantID
	} else if !validText(rec.Audience, maxAudienceBytes) {
		return TenantRecord{}, nil, fmt.Errorf("%w: invalid audience", ErrInvalidTenantRecord)
	}

	resources := make(map[string]struct{}, len(rec.SourceResources))
	for _, resource := range rec.SourceResources {
		if !validText(resource, maxResourceBytes) {
			return TenantRecord{}, nil, fmt.Errorf("%w: invalid source resource", ErrInvalidTenantRecord)
		}
		if _, duplicate := resources[resource]; duplicate {
			return TenantRecord{}, nil, fmt.Errorf("%w: duplicate source resource", ErrInvalidTenantRecord)
		}
		resources[resource] = struct{}{}
	}
	rec = cloneRecord(rec)
	return rec, resources, nil
}
