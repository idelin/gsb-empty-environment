package netsec

import (
	"context"
	"errors"
	"time"
)

// Sentinel errors returned by the task-scope service. Callers should use
// errors.Is to distinguish the different rejection reasons.
var (
	// ErrPermissionDenied means the principal has no permission on the
	// requested tenant/dataset pair.
	ErrPermissionDenied = errors.New("netsec: permission denied for dataset")
	// ErrConditionRewritten means a requested filter contradicts a mandatory
	// permission constraint and would widen the authorized scope.
	ErrConditionRewritten = errors.New("netsec: filter condition rewrites permitted scope")
	// ErrFilterTampered means the filters persisted on a task no longer match
	// their keyed binding, i.e. the stored condition was altered.
	ErrFilterTampered = errors.New("netsec: task filter binding verification failed")
	// ErrScopeExceeded means permissions were tightened after task creation and
	// the stored scope is no longer fully covered by current permissions.
	ErrScopeExceeded = errors.New("netsec: task scope exceeds current permission scope")
	// ErrCrossTenant means a principal from another tenant attempts to use a
	// task or a permission bound to a different tenant.
	ErrCrossTenant = errors.New("netsec: cross-tenant task access rejected")
	// ErrTaskNotOwned means a principal different from the task owner attempts
	// to execute the task within the same tenant.
	ErrTaskNotOwned = errors.New("netsec: task belongs to another principal")
	// ErrTaskReused means a task has already been claimed or consumed and
	// cannot be executed again.
	ErrTaskReused = errors.New("netsec: export task is single-use and cannot be reused")
	// ErrTaskNotFound means no task exists for the given identifier.
	ErrTaskNotFound = errors.New("netsec: export task not found")
	// ErrTaskExpired means the task passed its expiry time before execution.
	ErrTaskExpired = errors.New("netsec: export task expired")
)

// TaskStatus is the lifecycle state of an export task.
type TaskStatus string

const (
	StatusPending  TaskStatus = "pending"
	StatusClaimed  TaskStatus = "claimed"
	StatusConsumed TaskStatus = "consumed"
)

// Principal is the authenticated caller bound to a single tenant.
type Principal struct {
	ID       string
	TenantID string
}

// Filters are equality predicates applied to a dataset. Keys are column
// names, values are the only permitted column values.
type Filters map[string]string

// Permission describes one dataset grant for a principal. Constraints are
// mandatory equality predicates: an export may never read rows that violate
// them, and requesting a different value for a constrained key is rejected.
type Permission struct {
	TenantID    string
	Dataset     string
	Constraints Filters
}

// PermissionResolver returns the current permissions of a principal. It is
// consulted both at task creation and again immediately before execution so
// that permission revocation or tightening always takes effect.
type PermissionResolver interface {
	PermissionsFor(ctx context.Context, p Principal) ([]Permission, error)
}

// PermissionResolverFunc adapts a plain function to PermissionResolver.
type PermissionResolverFunc func(ctx context.Context, p Principal) ([]Permission, error)

// PermissionsFor implements PermissionResolver.
func (f PermissionResolverFunc) PermissionsFor(ctx context.Context, p Principal) ([]Permission, error) {
	return f(ctx, p)
}

// Task is an export job bound to an owner, tenant, dataset and an immutable,
// keyed filter set. Tasks are single-use: once executed they are consumed.
type Task struct {
	ID          string
	PrincipalID string
	TenantID    string
	Dataset     string
	Filters     Filters
	Status      TaskStatus
	CreatedAt   time.Time
	ExpiresAt   time.Time

	// binding is the HMAC over the immutable task fields, including the
	// canonical filter set. It is populated by the service and verified before
	// every execution.
	binding string
}

// Clone returns a deep copy of the task so callers cannot mutate stored state
// through returned pointers.
func (t *Task) Clone() *Task {
	if t == nil {
		return nil
	}
	cp := *t
	cp.Filters = t.Filters.Clone()
	return &cp
}

// Clone returns a detached copy of the filter set.
func (f Filters) Clone() Filters {
	if f == nil {
		return nil
	}
	cp := make(Filters, len(f))
	for k, v := range f {
		cp[k] = v
	}
	return cp
}
