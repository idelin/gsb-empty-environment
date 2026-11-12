// Package netsec validates libvirt <secret> references that tenants embed in
// domain XML (disk <auth> and <encryption> elements) against a management-plane
// secret registry before a domain is defined or started.
//
// The package enforces four security boundaries:
//
//   - A tenant may only reference secrets owned by that tenant. References by
//     UUID to another tenant's secret are rejected rather than silently resolved.
//   - References by usage name are resolved only among the calling tenant's
//     secrets and must match exactly one secret; an ambiguous usage is rejected.
//   - Private secret values are never exposed through any query result.
//   - Ephemeral secrets are not persisted. After a management-plane restart they
//     are gone and references to them fail until the secret is re-registered.
//
// The domain UUID and secret UUIDs live in separate namespaces. A domain whose
// domain UUID happens to equal a secret UUID does not grant or deny access to
// that secret; ownership checks always run against the registry.
package netsec

import (
	"errors"
	"regexp"
	"strings"
	"sync"
)

// Sentinel errors. Callers should compare with errors.Is rather than matching
// error text. Wrapped errors returned by the validation entry points preserve
// these sentinels.
var (
	// ErrSecretNotFound is returned when no visible secret matches the
	// reference. Cross-tenant references collapse to this so the registry does
	// not disclose the existence of another tenant's secret.
	ErrSecretNotFound = errors.New("netsec: secret not found")

	// ErrCrossTenant is returned when a UUID reference resolves to a secret
	// owned by a different tenant.
	ErrCrossTenant = errors.New("netsec: secret belongs to another tenant")

	// ErrUsageAmbiguous is returned when a usage reference matches more than
	// one secret within the calling tenant.
	ErrUsageAmbiguous = errors.New("netsec: secret usage is ambiguous")

	// ErrPrivateValueNotReadable is returned by value-returning queries for a
	// private secret. The reference itself may still be valid; only reading the
	// value is forbidden.
	ErrPrivateValueNotReadable = errors.New("netsec: private secret value is not readable")

	// ErrEphemeralUnavailable is returned when a referenced secret was
	// ephemeral and is no longer available because the management plane
	// restarted without persisting it.
	ErrEphemeralUnavailable = errors.New("netsec: ephemeral secret is unavailable after restart")

	// ErrInvalidReference is returned when a <secret> element carries neither
	// a uuid nor a usage attribute, or carries both, or a UUID is malformed.
	ErrInvalidReference = errors.New("netsec: invalid secret reference")

	// ErrMalformedXML is returned when the domain XML fragment cannot be parsed.
	ErrMalformedXML = errors.New("netsec: malformed domain XML")

	// ErrSecretAlreadyRegistered is returned when Register is called with a
	// UUID that already exists in the registry.
	ErrSecretAlreadyRegistered = errors.New("netsec: secret already registered")
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// IsValidUUID reports whether s is a canonical 8-4-4-4-12 libvirt UUID.
func IsValidUUID(s string) bool {
	return uuidPattern.MatchString(strings.TrimSpace(s))
}

// Secret is the public registration descriptor supplied by the management
// plane. Value is write-only input: it is never returned through any query
// result of this package.
type Secret struct {
	// UUID is the unique secret identifier, in canonical libvirt form.
	UUID string
	// Usage is the optional human-readable usage name referenced via
	// <secret usage='...'/>. It need not be globally unique.
	Usage string
	// TenantID identifies the owning tenant.
	TenantID string
	// Private marks a secret whose value must never be read back.
	Private bool
	// Ephemeral marks a secret that must not be persisted across restarts.
	Ephemeral bool
	// Value holds the secret material (password, Ceph key, LUKS passphrase).
	Value string
}

// SecretInfo is the safe, value-free projection of a registered secret used in
// every query result. It deliberately contains no secret material.
type SecretInfo struct {
	UUID      string
	Usage     string
	TenantID  string
	Private   bool
	Ephemeral bool
}

func infoOf(s storedSecret) SecretInfo {
	return SecretInfo{
		UUID:      s.UUID,
		Usage:     s.Usage,
		TenantID:  s.TenantID,
		Private:   s.Private,
		Ephemeral: s.Ephemeral,
	}
}

// storedSecret adds lifecycle state that must not be exposed.
type storedSecret struct {
	Secret
	// available is false for ephemeral secrets tombstoned by a restart.
	available bool
}

// Registry is the in-memory secret registry. It is safe for concurrent use.
type Registry struct {
	mu      sync.RWMutex
	secrets map[string]storedSecret
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{secrets: make(map[string]storedSecret)}
}

// Register adds or replaces a secret. Registering an ephemeral secret makes it
// immediately available; it is dropped again by Restart.
func (r *Registry) Register(s Secret) error {
	if !IsValidUUID(s.UUID) {
		return ErrInvalidReference
	}
	if strings.TrimSpace(s.TenantID) == "" {
		return ErrInvalidReference
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, exists := r.secrets[s.UUID]; exists && existing.available {
		return ErrSecretAlreadyRegistered
	}
	r.secrets[s.UUID] = storedSecret{Secret: s, available: true}
	return nil
}

// Restart models a management-plane restart. Every ephemeral secret is removed
// from the live set because it was never persisted; it becomes a tombstone so
// later references fail with ErrEphemeralUnavailable rather than masquerading
// as an unknown UUID. Durable secrets survive unchanged.
func (r *Registry) Restart() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for uuid, s := range r.secrets {
		if s.Ephemeral {
			s.available = false
			r.secrets[uuid] = s
		}
	}
}

// PersistableSecrets returns the value-free descriptors of every secret that
// survives a restart. Ephemeral secrets are excluded, which is the mechanism
// that keeps them off disk.
func (r *Registry) PersistableSecrets() []SecretInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []SecretInfo
	for _, s := range r.secrets {
		if s.Ephemeral {
			continue
		}
		out = append(out, infoOf(s))
	}
	return out
}

// ListForTenant returns value-free descriptors of the tenant's currently
// available secrets. No secret material is ever included.
func (r *Registry) ListForTenant(tenantID string) []SecretInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []SecretInfo
	for _, s := range r.secrets {
		if s.available && s.TenantID == tenantID {
			out = append(out, infoOf(s))
		}
	}
	return out
}

// lookupByUUID resolves a UUID reference, enforcing availability and ownership.
// Caller holds no lock; the method takes the read lock itself.
func (r *Registry) lookupByUUID(uuid, tenantID string) (storedSecret, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.secrets[uuid]
	if !ok {
		return storedSecret{}, ErrSecretNotFound
	}
	if !s.available {
		return storedSecret{}, ErrEphemeralUnavailable
	}
	if s.TenantID != tenantID {
		return storedSecret{}, ErrCrossTenant
	}
	return s, nil
}

// lookupByUsage resolves a usage reference. Only the calling tenant's secrets
// are considered, so another tenant's same-named secret can neither satisfy
// nor poison the lookup. Zero matches -> not found; several -> ambiguous.
func (r *Registry) lookupByUsage(usage, tenantID string) (storedSecret, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var matches []storedSecret
	var tombstoned []storedSecret
	for _, s := range r.secrets {
		if s.TenantID != tenantID || s.Usage != usage {
			continue
		}
		if !s.available {
			tombstoned = append(tombstoned, s)
			continue
		}
		matches = append(matches, s)
	}
	switch len(matches) {
	case 0:
		if len(tombstoned) > 0 {
			return storedSecret{}, ErrEphemeralUnavailable
		}
		return storedSecret{}, ErrSecretNotFound
	case 1:
		return matches[0], nil
	default:
		return storedSecret{}, ErrUsageAmbiguous
	}
}

// resolve is the shared ownership-aware lookup behind every public entry point.
func (r *Registry) resolve(ref SecretRef, tenantID string) (storedSecret, error) {
	switch ref.Kind {
	case RefUUID:
		if !IsValidUUID(ref.UUID) {
			return storedSecret{}, ErrInvalidReference
		}
		return r.lookupByUUID(ref.UUID, tenantID)
	case RefUsage:
		if strings.TrimSpace(ref.Usage) == "" {
			return storedSecret{}, ErrInvalidReference
		}
		return r.lookupByUsage(ref.Usage, tenantID)
	default:
		return storedSecret{}, ErrInvalidReference
	}
}

// Resolve looks up one reference for a tenant and returns a value-free
// descriptor. It never returns secret material, including for private secrets.
func (r *Registry) Resolve(ref SecretRef, tenantID string) (SecretInfo, error) {
	s, err := r.resolve(ref, tenantID)
	if err != nil {
		return SecretInfo{}, err
	}
	return infoOf(s), nil
}

// GetValue returns the material of a referenced secret. Private secrets always
// fail with ErrPrivateValueNotReadable, regardless of ownership, so a private
// value can never appear in a query result.
func (r *Registry) GetValue(ref SecretRef, tenantID string) (string, error) {
	s, err := r.resolve(ref, tenantID)
	if err != nil {
		return "", err
	}
	if s.Private {
		return "", ErrPrivateValueNotReadable
	}
	return s.Value, nil
}
