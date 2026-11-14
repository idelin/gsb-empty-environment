package netsec

import (
	"context"
	"fmt"
	"sort"
	"sync"
)

// Store is the read interface required by Engine. Implementations must scope
// lookups by tenant. Engine still re-checks tenant, patient ownership, and the
// resource-type allowlist on every value returned by the store.
type Store interface {
	Get(ctx context.Context, tenantID, resourceType, resourceID string) (*Resource, error)
	List(ctx context.Context, tenantID, resourceType string) ([]*Resource, error)
}

// ResourceStore is an alias for Store.
type ResourceStore = Store

// ReferenceIndex is an optional optimization for _revinclude. The engine
// verifies the returned resources just like it does with List results.
type ReferenceIndex interface {
	FindReferencing(ctx context.Context, tenantID, sourceType string, target Reference) ([]*Resource, error)
}

// MemoryStore is a concurrency-safe in-memory Store. It is useful for tests
// and for small embedded services.
type MemoryStore struct {
	mu        sync.RWMutex
	resources map[string]*Resource
}

// NewMemoryStore returns a store populated with resources.
func NewMemoryStore(resources ...*Resource) *MemoryStore {
	store := &MemoryStore{resources: make(map[string]*Resource)}
	for _, resource := range resources {
		store.Put(resource)
	}
	return store
}

// Put inserts or replaces a resource.
func (m *MemoryStore) Put(resource *Resource) {
	if resource == nil || resource.Type == "" || resource.ID == "" || resource.TenantID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.resources == nil {
		m.resources = make(map[string]*Resource)
	}
	m.resources[storeKey(resource.TenantID, resource.Type, resource.ID)] = resource
}

// Delete removes a resource.
func (m *MemoryStore) Delete(tenantID, resourceType, resourceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.resources, storeKey(tenantID, resourceType, resourceID))
}

// Len returns the number of stored resources.
func (m *MemoryStore) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.resources)
}

// Get implements Store.
func (m *MemoryStore) Get(ctx context.Context, tenantID, resourceType, resourceID string) (*Resource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	resource := m.resources[storeKey(tenantID, resourceType, resourceID)]
	m.mu.RUnlock()
	if resource == nil {
		return nil, fmt.Errorf("%w: %s/%s in tenant %s", ErrNotFound, resourceType, resourceID, tenantID)
	}
	return resource, nil
}

// List implements Store.
func (m *MemoryStore) List(ctx context.Context, tenantID, resourceType string) ([]*Resource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	resources := make([]*Resource, 0)
	for _, resource := range m.resources {
		if resource.TenantID == tenantID && resource.Type == resourceType {
			resources = append(resources, resource)
		}
	}
	m.mu.RUnlock()
	sortResources(resources)
	return resources, nil
}

// FindReferencing implements ReferenceIndex.
func (m *MemoryStore) FindReferencing(ctx context.Context, tenantID, sourceType string, target Reference) ([]*Resource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	resources := make([]*Resource, 0)
	for _, resource := range m.resources {
		if resource.TenantID != tenantID || resource.Type != sourceType {
			continue
		}
		if referencesTarget(resource, target) {
			resources = append(resources, resource)
		}
	}
	m.mu.RUnlock()
	sortResources(resources)
	return resources, nil
}

func referencesTarget(resource *Resource, target Reference) bool {
	for _, refs := range []map[string][]string{resource.Attributes, resource.References} {
		for _, values := range refs {
			for _, raw := range values {
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
		}
	}
	return false
}

func storeKey(tenantID, resourceType, resourceID string) string {
	return tenantID + "\x00" + resourceType + "\x00" + resourceID
}

func sortResources(resources []*Resource) {
	sort.SliceStable(resources, func(i, j int) bool {
		if resources[i].Type != resources[j].Type {
			return resources[i].Type < resources[j].Type
		}
		return resources[i].ID < resources[j].ID
	})
}
