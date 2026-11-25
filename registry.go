package credentialhost

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxSessionSeconds = int64(math.MaxInt64 / int64(time.Second))

type tenantState struct {
	record  TenantRecord
	allowed map[string]struct{}
}

type resourceState struct {
	owner   string
	version uint64
}

// Registry owns tenant registrations, their generation versions, and the
// current source-resource ownership map.
//
// Every registration or rotation increments the tenant version. Revocation also
// increments it and marks the tenant permanently revoked. Resource transfers
// increment that resource's version. Credential caching and in-flight results
// are bound to both versions.
type Registry struct {
	mu           sync.RWMutex
	tenants      map[string]*tenantState
	resources    map[string]*resourceState
	listeners    map[uint64]func(string)
	nextListener uint64
}

// NewRegistry returns an empty tenant registry.
func NewRegistry() *Registry {
	return &Registry{
		tenants:   make(map[string]*tenantState),
		resources: make(map[string]*resourceState),
		listeners: make(map[uint64]func(string)),
	}
}

// RegisterTenant registers a new tenant and generates its random 128-bit
// external ID. Registering an existing tenant, including a revoked one, fails
// with ErrTenantExists.
func (r *Registry) RegisterTenant(config TenantConfig) (TenantRecord, error) {
	record, allowed, err := normalizeTenantConfig(config)
	if err != nil {
		return TenantRecord{}, err
	}
	externalID, err := randomExternalID()
	if err != nil {
		return TenantRecord{}, fmt.Errorf("credentialhost: generate external ID: %w", err)
	}
	record.ExternalID = externalID
	record.Version = 1

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tenants[record.TenantID]; exists {
		return TenantRecord{}, ErrTenantExists
	}
	for resource := range allowed {
		if current, exists := r.resources[resource]; exists && current.owner != "" && current.owner != record.TenantID {
			return TenantRecord{}, fmt.Errorf("%w: resource %q is owned by another tenant", ErrResourceOwnerMismatch, resource)
		}
	}

	record.SourceResources = sortedKeys(allowed)
	r.tenants[record.TenantID] = &tenantState{
		record:  cloneTenantRecord(record),
		allowed: cloneSet(allowed),
	}
	for resource := range allowed {
		current, exists := r.resources[resource]
		if !exists {
			r.resources[resource] = &resourceState{owner: record.TenantID, version: 1}
			continue
		}
		if current.owner != record.TenantID {
			current.owner = record.TenantID
			current.version++
		}
	}
	return cloneTenantRecord(record), nil
}

// ReplaceTenant rotates an existing tenant to a new configuration generation.
// A fresh random external ID is generated, the tenant version is incremented,
// and the old generation is invalidated for cache and in-flight purposes.
func (r *Registry) ReplaceTenant(config TenantConfig) (TenantRecord, error) {
	record, allowed, err := normalizeTenantConfig(config)
	if err != nil {
		return TenantRecord{}, err
	}
	externalID, err := randomExternalID()
	if err != nil {
		return TenantRecord{}, fmt.Errorf("credentialhost: generate external ID: %w", err)
	}

	r.mu.Lock()
	current, exists := r.tenants[record.TenantID]
	if !exists {
		r.mu.Unlock()
		return TenantRecord{}, ErrTenantNotFound
	}
	if current.record.Revoked {
		r.mu.Unlock()
		return TenantRecord{}, ErrTenantRevoked
	}
	for resource := range allowed {
		if owner, exists := r.resources[resource]; exists && owner.owner != "" && owner.owner != record.TenantID {
			r.mu.Unlock()
			return TenantRecord{}, fmt.Errorf("%w: resource %q is owned by another tenant", ErrResourceOwnerMismatch, resource)
		}
	}

	record.ExternalID = externalID
	record.Version = current.record.Version + 1
	record.SourceResources = sortedKeys(allowed)

	for resource := range current.allowed {
		if _, remainsAllowed := allowed[resource]; remainsAllowed {
			continue
		}
		if owner, exists := r.resources[resource]; exists && owner.owner == record.TenantID {
			owner.owner = ""
			owner.version++
		}
	}
	for resource := range allowed {
		owner, exists := r.resources[resource]
		if !exists {
			r.resources[resource] = &resourceState{owner: record.TenantID, version: 1}
			continue
		}
		if owner.owner != record.TenantID {
			owner.owner = record.TenantID
			owner.version++
		}
	}
	current.record = cloneTenantRecord(record)
	current.allowed = cloneSet(allowed)
	r.mu.Unlock()

	r.notifyTenantChanged(record.TenantID)
	return cloneTenantRecord(record), nil
}

// RotateTenant is an alias for ReplaceTenant.
func (r *Registry) RotateTenant(config TenantConfig) (TenantRecord, error) {
	return r.ReplaceTenant(config)
}

// RevokeTenant permanently revokes a tenant generation. Revocation is
// idempotent. It does not transfer resource ownership.
func (r *Registry) RevokeTenant(tenantID string) error {
	r.mu.Lock()
	current, exists := r.tenants[tenantID]
	if !exists {
		r.mu.Unlock()
		return ErrTenantNotFound
	}
	if current.record.Revoked {
		r.mu.Unlock()
		return nil
	}
	current.record.Revoked = true
	current.record.Version++
	r.mu.Unlock()

	r.notifyTenantChanged(tenantID)
	return nil
}

// GetTenant returns a copy of the current tenant record.
func (r *Registry) GetTenant(tenantID string) (TenantRecord, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	current, exists := r.tenants[tenantID]
	if !exists {
		return TenantRecord{}, false
	}
	return cloneTenantRecord(current.record), true
}

// SetResourceOwner transfers resource ownership. An empty tenantID clears the
// owner. Transferring a resource invalidates in-flight and cached decisions that
// used the previous owner.
func (r *Registry) SetResourceOwner(resource, tenantID string) error {
	if !validExactString(resource) {
		return fmt.Errorf("%w: source resource must be non-empty without surrounding whitespace", ErrInvalidTenantConfig)
	}
	if tenantID != "" && !validExactString(tenantID) {
		return fmt.Errorf("%w: tenant ID must have no surrounding whitespace", ErrInvalidTenantConfig)
	}

	r.mu.Lock()
	if tenantID != "" {
		target, exists := r.tenants[tenantID]
		if !exists {
			r.mu.Unlock()
			return ErrTenantNotFound
		}
		if target.record.Revoked {
			r.mu.Unlock()
			return ErrTenantRevoked
		}
	}
	current, exists := r.resources[resource]
	previousOwner := ""
	if exists {
		previousOwner = current.owner
	}
	if previousOwner == tenantID {
		r.mu.Unlock()
		return nil
	}
	if !exists {
		if tenantID == "" {
			r.mu.Unlock()
			return nil
		}
		r.resources[resource] = &resourceState{owner: tenantID, version: 1}
	} else {
		current.owner = tenantID
		current.version++
	}
	r.mu.Unlock()

	if previousOwner != "" {
		r.notifyTenantChanged(previousOwner)
	}
	return nil
}

// TransferResource is an alias for SetResourceOwner.
func (r *Registry) TransferResource(resource, tenantID string) error {
	return r.SetResourceOwner(resource, tenantID)
}

// ResourceOwner returns the current owner of resource.
func (r *Registry) ResourceOwner(resource string) (tenantID string, ok bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	current, exists := r.resources[resource]
	if !exists || current.owner == "" {
		return "", false
	}
	return current.owner, true
}

func (r *Registry) authorizeLocked(request AssumeRequest, principal Principal, now time.Time) (Decision, error) {
	if !principal.ownsTenant(request.TenantID) {
		return Decision{}, ErrTenantClaimMismatch
	}
	tenant, exists := r.tenants[request.TenantID]
	if !exists {
		return Decision{}, ErrTenantNotFound
	}
	if tenant.record.Revoked {
		return Decision{}, ErrTenantRevoked
	}
	if request.RoleID != tenant.record.RoleID {
		return Decision{}, ErrRoleMismatch
	}
	if !constantTimeEqual(request.ExternalID, tenant.record.ExternalID) {
		return Decision{}, ErrExternalIDMismatch
	}
	if _, allowed := tenant.allowed[request.SourceResource]; !allowed {
		return Decision{}, ErrSourceResourceNotAllowed
	}
	resource, exists := r.resources[request.SourceResource]
	if !exists || resource.owner != request.TenantID {
		return Decision{}, ErrResourceOwnerMismatch
	}
	if request.DurationSeconds <= 0 {
		return Decision{}, ErrInvalidDuration
	}
	if request.DurationSeconds > tenant.record.MaxSessionSeconds {
		return Decision{}, fmt.Errorf("%w: exceeds tenant maximum of %d seconds", ErrInvalidDuration, tenant.record.MaxSessionSeconds)
	}
	if request.DurationSeconds > maxSessionSeconds {
		return Decision{}, fmt.Errorf("%w: duration overflows time.Duration", ErrInvalidDuration)
	}

	decision := Decision{
		Principal:        principal.Subject,
		TenantID:         request.TenantID,
		RoleID:           request.RoleID,
		ExternalID:       request.ExternalID,
		SourceResource:   request.SourceResource,
		DurationSeconds:  request.DurationSeconds,
		ParametersDigest: request.ParametersDigest,
		Audience:         tenant.record.TenantID,
		SourceIdentity:   principal.Subject,
		NotAfter:         now.Add(time.Duration(request.DurationSeconds) * time.Second),
		PolicyDigest:     tenant.record.PolicyDigest,
		TenantVersion:    tenant.record.Version,
		ResourceVersion:  resource.version,
		RequestID:        request.RequestID,
		SessionName:      request.SessionName,
	}
	decision.Digest = ComputeDecisionDigest(decision)
	decision.CanonicalDecision = CanonicalDecision(decision)
	decision.SessionID = expectedSessionID(
		request.RequestID,
		decision.Digest,
		decision.TenantVersion,
		decision.ResourceVersion,
	)
	return decision, nil
}

func (r *Registry) stillCurrentLocked(decision Decision) bool {
	tenant, exists := r.tenants[decision.TenantID]
	if !exists || tenant.record.Revoked {
		return false
	}
	if tenant.record.Version != decision.TenantVersion {
		return false
	}
	if tenant.record.RoleID != decision.RoleID ||
		tenant.record.ExternalID != decision.ExternalID ||
		tenant.record.PolicyDigest != decision.PolicyDigest {
		return false
	}
	if decision.DurationSeconds <= 0 || decision.DurationSeconds > tenant.record.MaxSessionSeconds {
		return false
	}
	if _, allowed := tenant.allowed[decision.SourceResource]; !allowed {
		return false
	}
	resource, exists := r.resources[decision.SourceResource]
	if !exists || resource.owner != decision.TenantID || resource.version != decision.ResourceVersion {
		return false
	}
	return true
}

func (r *Registry) addChangeListener(listener func(string)) func() {
	r.mu.Lock()
	id := r.nextListener + 1
	r.nextListener = id
	r.listeners[id] = listener
	r.mu.Unlock()

	return func() {
		r.mu.Lock()
		delete(r.listeners, id)
		r.mu.Unlock()
	}
}

func (r *Registry) notifyTenantChanged(tenantID string) {
	r.mu.RLock()
	listeners := make([]func(string), 0, len(r.listeners))
	for _, listener := range r.listeners {
		listeners = append(listeners, listener)
	}
	r.mu.RUnlock()
	for _, listener := range listeners {
		listener(tenantID)
	}
}

func normalizeTenantConfig(config TenantConfig) (TenantRecord, map[string]struct{}, error) {
	if !validExactString(config.TenantID) {
		return TenantRecord{}, nil, fmt.Errorf("%w: tenant ID must be non-empty without surrounding whitespace", ErrInvalidTenantConfig)
	}
	if !validExactString(config.RoleID) {
		return TenantRecord{}, nil, fmt.Errorf("%w: role ID must be non-empty without surrounding whitespace", ErrInvalidTenantConfig)
	}
	if config.MaxSessionSeconds <= 0 || config.MaxSessionSeconds > maxSessionSeconds {
		return TenantRecord{}, nil, fmt.Errorf("%w: max session seconds must be between 1 and %d", ErrInvalidTenantConfig, maxSessionSeconds)
	}
	if !validExactString(config.PolicyDigest) {
		return TenantRecord{}, nil, fmt.Errorf("%w: policy digest must be non-empty without surrounding whitespace", ErrInvalidTenantConfig)
	}

	allowed := make(map[string]struct{}, len(config.SourceResources))
	for _, resource := range config.SourceResources {
		if !validExactString(resource) {
			return TenantRecord{}, nil, fmt.Errorf("%w: source resources must be non-empty without surrounding whitespace", ErrInvalidTenantConfig)
		}
		allowed[resource] = struct{}{}
	}
	return TenantRecord{
		TenantID:          config.TenantID,
		RoleID:            config.RoleID,
		SourceResources:   sortedKeys(allowed),
		MaxSessionSeconds: config.MaxSessionSeconds,
		PolicyDigest:      config.PolicyDigest,
	}, allowed, nil
}

func randomExternalID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func cloneTenantRecord(record TenantRecord) TenantRecord {
	record.SourceResources = append([]string(nil), record.SourceResources...)
	return record
}

func cloneSet(set map[string]struct{}) map[string]struct{} {
	clone := make(map[string]struct{}, len(set))
	for value := range set {
		clone[value] = struct{}{}
	}
	return clone
}

func sortedKeys(set map[string]struct{}) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortStrings(values []string) {
	sort.Strings(values)
}

func uniqueStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

func u64String(value uint64) string {
	return strconv.FormatUint(value, 10)
}

func validExactString(value string) bool {
	return value != "" && strings.TrimSpace(value) == value
}

func validateSessionName(sessionName string) error {
	if len(sessionName) < 1 || len(sessionName) > 64 {
		return fmt.Errorf("%w: must be 1 to 64 bytes", ErrInvalidSessionName)
	}
	for i := 0; i < len(sessionName); i++ {
		if !safeSessionNameByte(sessionName[i]) {
			return fmt.Errorf("%w: must contain only letters, digits, or +=,.@_-", ErrInvalidSessionName)
		}
	}
	return nil
}

func safeSessionNameByte(value byte) bool {
	switch {
	case value >= 'a' && value <= 'z':
		return true
	case value >= 'A' && value <= 'Z':
		return true
	case value >= '0' && value <= '9':
		return true
	}
	switch value {
	case '+', '=', ',', '.', '@', '_', '-':
		return true
	default:
		return false
	}
}

func constantTimeEqual(left, right string) bool {
	leftHash := sha256.Sum256([]byte(left))
	rightHash := sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(leftHash[:], rightHash[:]) == 1
}
