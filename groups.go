package netsec

import "fmt"

// BindGroup registers a consumer group name under its owning principal. The
// caller must first have passed Authorize for the group resource with
// ActionAdmin. Binding an already registered name to a different principal
// fails with ErrGroupOwnerConflict: a cross-tenant group name cannot be
// taken over or recycled through a rename.
func (a *Authorizer) BindGroup(principal, groupName string) error {
	if principal == "" {
		return ErrEmptyPrincipal
	}
	if err := validateName(groupName, "group name", a.delim); err != nil {
		return err
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	owner, registered := a.groups[groupName]
	if registered && owner != principal {
		return fmt.Errorf("%w: group %q is owned by %q", ErrGroupOwnerConflict, groupName, owner)
	}
	if !registered {
		a.groups[groupName] = principal
	}
	return nil
}

// RenameGroup moves a group name for the same owning principal. Both the old
// and the new name must be authorized for ActionAdmin under the current ACL
// generation. A rename onto a name already owned by another principal is
// rejected so tenant boundaries cannot be bypassed by reusing a foreign name.
func (a *Authorizer) RenameGroup(principal, oldName, newName string) error {
	if principal == "" {
		return ErrEmptyPrincipal
	}
	if err := validateName(oldName, "group name", a.delim); err != nil {
		return err
	}
	if err := validateName(newName, "group name", a.delim); err != nil {
		return err
	}

	a.mu.Lock()
	owner, ok := a.groups[oldName]
	if !ok || owner != principal {
		a.mu.Unlock()
		return fmt.Errorf("%w: group %q is not owned by %q", ErrGroupNotFound, oldName, principal)
	}
	if existingOwner, taken := a.groups[newName]; taken && existingOwner != principal {
		a.mu.Unlock()
		return fmt.Errorf("%w: group %q is owned by %q", ErrGroupOwnerConflict, newName, existingOwner)
	}
	rules := a.current
	a.mu.Unlock()

	for _, name := range []string{oldName, newName} {
		resource := Resource{Kind: KindGroup, Name: name}
		if err := a.authorizeAgainst(rules, principal, resource, ActionAdmin); err != nil {
			return err
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	// Re-check under the write lock in case the generation or ownership moved.
	if a.current.generation != rules.generation {
		return fmt.Errorf("%w", ErrSubscriptionStale)
	}
	if owner, ok := a.groups[oldName]; !ok || owner != principal {
		return fmt.Errorf("%w: group %q is not owned by %q", ErrGroupNotFound, oldName, principal)
	}
	if existingOwner, taken := a.groups[newName]; taken && existingOwner != principal {
		return fmt.Errorf("%w: group %q is owned by %q", ErrGroupOwnerConflict, newName, existingOwner)
	}
	delete(a.groups, oldName)
	a.groups[newName] = principal
	return nil
}

// GroupOwner returns the principal that currently owns a group name.
func (a *Authorizer) GroupOwner(groupName string) (string, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	owner, ok := a.groups[groupName]
	return owner, ok
}

// ReleaseGroup removes a group binding. Only its current owner may release it.
func (a *Authorizer) ReleaseGroup(principal, groupName string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	owner, ok := a.groups[groupName]
	if !ok {
		return fmt.Errorf("%w: group %q", ErrGroupNotFound, groupName)
	}
	if owner != principal {
		return fmt.Errorf("%w: group %q is owned by %q", ErrGroupOwnerConflict, groupName, owner)
	}
	delete(a.groups, groupName)
	return nil
}

// authorizeAgainst evaluates a request against a specific snapshot without
// touching the cache. It backs generation-sensitive operations such as
// rename and subscription re-verification.
func (a *Authorizer) authorizeAgainst(snap snapshot, principal string, resource Resource, actions Action) error {
	if principal == "" {
		return fmt.Errorf("%w: %v", ErrNotAuthorized, ErrEmptyPrincipal)
	}
	if actions == 0 || actions&^validActionMask != 0 {
		return fmt.Errorf("%w: %v", ErrNotAuthorized, ErrUnknownAction)
	}
	if err := resource.Valid(a.delim); err != nil {
		return fmt.Errorf("%w: %v", ErrNotAuthorized, err)
	}
	a.mu.RLock()
	allowed := evaluate(snap, principal, resource, actions)
	a.mu.RUnlock()
	if allowed {
		return nil
	}
	return fmt.Errorf("%w: principal %q denied %s %q", ErrNotAuthorized, principal, resource.Kind, resource.Name)
}

// evaluate is the pure deny-over-allow evaluation over an immutable snapshot.
func evaluate(snap snapshot, principal string, resource Resource, actions Action) bool {
	var denied, allowed bool
	for _, entry := range snap.rules.entries(resource.Kind) {
		if entry.principal != principal || entry.actions&actions != actions {
			continue
		}
		if !entry.match(resource.Name) {
			continue
		}
		switch entry.effect {
		case EffectDeny:
			denied = true
		case EffectAllow:
			allowed = true
		}
	}
	return allowed && !denied
}
