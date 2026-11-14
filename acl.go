package netsec

import "sync"

// ACL is an in-memory Authorizer keyed by tenant, subject and topic resource.
// The resource "*" may be used as a wildcard grant for all resources of a
// tenant.
type ACL struct {
	mu     sync.RWMutex
	grants map[string]map[string]map[string]bool
}

// NewACL creates an empty ACL that denies everything until granted.
func NewACL() *ACL {
	return &ACL{grants: make(map[string]map[string]map[string]bool)}
}

// Grant allows subject to subscribe to resource within tenant.
func (a *ACL) Grant(tenantID, subjectID, resource string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	subjects, ok := a.grants[tenantID]
	if !ok {
		subjects = make(map[string]map[string]bool)
		a.grants[tenantID] = subjects
	}
	resources, ok := subjects[subjectID]
	if !ok {
		resources = make(map[string]bool)
		subjects[subjectID] = resources
	}
	resources[resource] = true
}

// Revoke removes a previously granted permission.
func (a *ACL) Revoke(tenantID, subjectID, resource string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if subjects, ok := a.grants[tenantID]; ok {
		if resources, ok := subjects[subjectID]; ok {
			delete(resources, resource)
		}
	}
}

// Authorize implements Authorizer. Topics of a foreign tenant are always
// denied, even if a grant exists under the caller's tenant.
func (a *ACL) Authorize(tenantID, subjectID string, topic Topic) error {
	if topic.TenantID != tenantID {
		return ErrCrossTenant
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	if resources, ok := a.grants[tenantID][subjectID]; ok {
		if resources[topic.Resource] || resources["*"] {
			return nil
		}
	}
	return ErrTopicDenied
}
