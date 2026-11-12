package relocation

import (
	"fmt"
	"strings"
	"sync"
)

// Registry records the namespace owners that a resolver caller trusts.
//
// An owner owns every groupId registered to it together with all descendant
// namespaces (for example an owner of "com.example" also owns
// "com.example.libs"). Explicit approvals are recorded per groupId and allow a
// relocation target outside the original owner's namespaces.
//
// Registry is safe for concurrent use.
type Registry struct {
	mu       sync.RWMutex
	owners   map[string]map[string]struct{}
	approved map[string]struct{}
}

// NewRegistry returns an empty owner registry.
func NewRegistry() *Registry {
	return &Registry{
		owners:   make(map[string]map[string]struct{}),
		approved: make(map[string]struct{}),
	}
}

// RegisterOwner records that owner owns the given groupId namespaces.
// Both the owner name and every namespace must be non-empty and pass
// groupId path validation.
func (r *Registry) RegisterOwner(owner string, namespaces ...string) error {
	if owner == "" {
		return fmt.Errorf("relocation: owner must not be empty")
	}
	if len(namespaces) == 0 {
		return fmt.Errorf("relocation: owner %q registered without namespaces", owner)
	}
	for _, namespace := range namespaces {
		if err := CheckGroupID(namespace); err != nil {
			return fmt.Errorf("relocation: invalid namespace for owner %q: %w", owner, err)
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	owned, ok := r.owners[owner]
	if !ok {
		owned = make(map[string]struct{})
		r.owners[owner] = owned
	}
	for _, namespace := range namespaces {
		owned[namespace] = struct{}{}
	}
	return nil
}

// Approve marks a groupId as an explicitly trusted relocation target even when
// it belongs to a different namespace owner.
func (r *Registry) Approve(groupID string) error {
	if err := CheckGroupID(groupID); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.approved[groupID] = struct{}{}
	return nil
}

// IsApproved reports whether groupID has been explicitly approved.
func (r *Registry) IsApproved(groupID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.approved[groupID]
	return ok
}

// OwnerOf returns the owner whose registered namespace is the longest prefix
// of groupID. It returns an empty string when no owner claims the groupId.
func (r *Registry) OwnerOf(groupID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	bestOwner := ""
	bestLen := -1
	for owner, namespaces := range r.owners {
		for namespace := range namespaces {
			if !hasNamespacePrefix(groupID, namespace) {
				continue
			}
			if len(namespace) > bestLen {
				bestLen = len(namespace)
				bestOwner = owner
			}
		}
	}
	return bestOwner
}

// Allowed reports whether a relocation from originalGroupID to targetGroupID is
// permitted. Both groupIds must be valid, they must share the same registered
// namespace owner, and an unregistered original groupId is rejected (fail
// closed). Explicitly approved targets always pass.
func (r *Registry) Allowed(originalGroupID, targetGroupID string) bool {
	if err := CheckGroupID(originalGroupID); err != nil {
		return false
	}
	if err := CheckGroupID(targetGroupID); err != nil {
		return false
	}
	if r.IsApproved(targetGroupID) {
		return true
	}

	originalOwner := r.OwnerOf(originalGroupID)
	if originalOwner == "" {
		return false
	}
	targetOwner := r.OwnerOf(targetGroupID)
	return targetOwner != "" && targetOwner == originalOwner
}

// hasNamespacePrefix reports whether groupID is namespace itself or a
// descendant namespace (the next character must be a segment boundary).
func hasNamespacePrefix(groupID, namespace string) bool {
	if groupID == namespace {
		return true
	}
	return strings.HasPrefix(groupID, namespace+".")
}
