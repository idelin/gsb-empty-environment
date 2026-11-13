// Package netsec provides authorization for io_uring sync-cancel requests on
// a ring shared by multiple mutually distrustful tenants.
//
// The host's IORING_REGISTER_SYNC_CANCEL registration path delegates every
// cancel request to the Authorizer before it is allowed to touch the shared
// ring. All matching is scoped to the caller's own tenant so that a tenant
// cannot observe or cancel requests belonging to another tenant, even when
// using wildcards or fixed-file slots.
package netsec

import (
	"errors"
	"sync"
	"time"
)

// MaxCancelTimeout is the largest sync-cancel timeout the host accepts. A
// longer timeout is rejected before any request is inspected.
const MaxCancelTimeout = 10 * time.Second

// CancelFlag mirrors the io_uring IORING_ASYNC_CANCEL_* selector flags used by
// IORING_REGISTER_SYNC_CANCEL.
type CancelFlag uint

const (
	// CancelAny matches every in-flight request of the caller. It cannot be
	// combined with any other selector flag.
	CancelAny CancelFlag = 1 << iota // IORING_ASYNC_CANCEL_ANY
	// CancelAll cancels every matching request instead of only the first one.
	CancelAll // IORING_ASYNC_CANCEL_ALL
	// CancelFD matches against the request file descriptor.
	CancelFD // IORING_ASYNC_CANCEL_FD
	// CancelFDFixed interprets FD as an index into the caller's fixed file
	// table rather than as an open file descriptor.
	CancelFDFixed // IORING_ASYNC_CANCEL_FD_FIXED
	// CancelOp matches against the request opcode.
	CancelOp // IORING_ASYNC_CANCEL_OP
	// CancelUserData matches against the request user_data.
	CancelUserData // IORING_ASYNC_CANCEL_USERDATA
)

// supportedFlags is the set of selectors the host understands. Unknown bits
// (for example IORING_ASYNC_CANCEL_ANY_WORK on a newer kernel) are rejected
// rather than silently ignored.
var supportedFlags = CancelAny | CancelAll | CancelFD | CancelFDFixed | CancelOp | CancelUserData

var (
	// ErrTimeoutTooLarge is returned when the requested timeout exceeds
	// MaxCancelTimeout.
	ErrTimeoutTooLarge = errors.New("netsec: sync cancel timeout exceeds 10s")
	// ErrInvalidTimeout is returned for a negative timeout.
	ErrInvalidTimeout = errors.New("netsec: negative sync cancel timeout")
	// ErrInvalidFlags is returned for unknown, contradictory or underspecified
	// selector flags.
	ErrInvalidFlags = errors.New("netsec: invalid sync cancel flags")
	// ErrInvalidFixedSlot is returned when an FD_FIXED index is not present in
	// the caller's fixed file table. The message never references another
	// tenant's table.
	ErrInvalidFixedSlot = errors.New("netsec: fixed file slot not registered for caller")
	// ErrNoMatch is returned when the caller has no matching in-flight request.
	// It is identical whether or not another tenant has a matching request, so
	// it cannot be used to probe another tenant's traffic.
	ErrNoMatch = errors.New("netsec: no matching request for caller")
)

// TenantID identifies a tenant sharing the ring.
type TenantID string

// Request is one in-flight submission recorded on the shared ring.
type Request struct {
	// ID is a host-assigned identifier used to report which requests were
	// authorized for cancellation.
	ID uint64
	// Tenant owns the request. Cancellation is always restricted to it.
	Tenant TenantID
	// UserData is the sqe user_data value.
	UserData uint64
	// FD is the request file descriptor. For a request that used a fixed file,
	// this is the host file descriptor the caller's slot resolved to.
	FD int32
	// Opcode is the sqe operation code.
	Opcode uint8
}

// CancelReq is a sync-cancel request as extracted from the registration path
// before authorization.
type CancelReq struct {
	// Caller is the tenant issuing the cancel request.
	Caller TenantID
	// Flags carries the IORING_ASYNC_CANCEL_* selectors.
	Flags CancelFlag
	// UserData is matched when CancelUserData is set.
	UserData uint64
	// FD is matched when CancelFD is set. With CancelFDFixed it is a fixed
	// file slot belonging to the caller.
	FD int32
	// Opcode is matched when CancelOp is set.
	Opcode uint8
	// Timeout bounds how long the kernel may wait for a match.
	Timeout time.Duration
}

// tenantState holds every piece of ring state belonging to one tenant. The
// cancel path never reaches into another tenant's state.
type tenantState struct {
	// requests maps host request ID to the in-flight request.
	requests map[uint64]Request
	// fixedFiles maps the tenant's fixed file slots to host file descriptors.
	fixedFiles map[int32]int32
}

func newTenantState() *tenantState {
	return &tenantState{
		requests:   make(map[uint64]Request),
		fixedFiles: make(map[int32]int32),
	}
}

// Authorizer authorizes sync-cancel requests for one shared ring. It is safe
// for concurrent use.
type Authorizer struct {
	mu      sync.RWMutex
	tenants map[TenantID]*tenantState
	nextID  uint64
}

// NewAuthorizer creates an empty Authorizer.
func NewAuthorizer() *Authorizer {
	return &Authorizer{tenants: make(map[TenantID]*tenantState)}
}

// stateFor returns the tenant's state, creating it on first use.
func (a *Authorizer) stateFor(tenant TenantID) *tenantState {
	st, ok := a.tenants[tenant]
	if !ok {
		st = newTenantState()
		a.tenants[tenant] = st
	}
	return st
}

// validateFlags enforces the kernel's flag grammar so that ambiguous requests
// never reach the matching stage.
func validateFlags(flags CancelFlag) error {
	if flags&^supportedFlags != 0 {
		return ErrInvalidFlags
	}
	if flags&CancelAny != 0 {
		// ANY is a wildcard and must not be combined with concrete selectors or
		// ALL.
		if flags&(CancelAll|CancelFD|CancelFDFixed|CancelOp|CancelUserData) != 0 {
			return ErrInvalidFlags
		}
		return nil
	}
	if flags&CancelFDFixed != 0 && flags&CancelFD == 0 {
		// A fixed-file qualifier only makes sense alongside an FD selector.
		return ErrInvalidFlags
	}
	if flags&(CancelFD|CancelOp|CancelUserData) == 0 {
		// Without ANY at least one concrete selector is required.
		return ErrInvalidFlags
	}
	return nil
}

// match reports whether req satisfies every concrete selector in c. The
// caller's fixed slot has already been resolved to hostFD.
func (c CancelReq) match(req Request, hostFD int32) bool {
	if c.Flags&CancelUserData != 0 && req.UserData != c.UserData {
		return false
	}
	if c.Flags&CancelOp != 0 && req.Opcode != c.Opcode {
		return false
	}
	if c.Flags&CancelFD != 0 && req.FD != hostFD {
		return false
	}
	return true
}

// SyncCancel authorizes a sync-cancel request for the caller and removes the
// authorized requests from the in-flight registry. It returns the number of
// requests cancelled.
//
// Only the caller's own requests are ever considered: wildcards (ANY/ALL)
// stay inside the caller's tenant, FD_FIXED slots resolve through the caller's
// own fixed file table, and no error distinguishes "no match for caller" from
// "another tenant has a match".
func (a *Authorizer) SyncCancel(c CancelReq) (int, error) {
	if c.Timeout < 0 {
		return 0, ErrInvalidTimeout
	}
	if c.Timeout > MaxCancelTimeout {
		return 0, ErrTimeoutTooLarge
	}
	if err := validateFlags(c.Flags); err != nil {
		return 0, err
	}
	if c.Caller == "" {
		return 0, ErrInvalidFlags
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	caller := a.stateFor(c.Caller)

	// Resolve an FD_FIXED slot through the caller's own table before touching
	// any request. A missing slot is reported without consulting another
	// tenant's table.
	hostFD := c.FD
	if c.Flags&CancelFDFixed != 0 {
		resolved, ok := caller.fixedFiles[c.FD]
		if !ok {
			return 0, ErrInvalidFixedSlot
		}
		hostFD = resolved
	}

	var matched []uint64

	for id, req := range caller.requests {
		// Defense in depth: never act on another tenant's request even if the
		// registry were populated incorrectly.
		if req.Tenant != c.Caller {
			continue
		}
		if c.Flags&CancelAny != 0 || c.match(req, hostFD) {
			matched = append(matched, id)
			// ANY cancels every request of the caller; concrete selectors
			// without ALL keep the kernel default of cancelling only the
			// first match.
			if c.Flags&(CancelAny|CancelAll) == 0 {
				break // Kernel default: cancel only the first match.
			}
		}
	}

	if len(matched) == 0 {
		return 0, ErrNoMatch
	}
	for _, id := range matched {
		delete(caller.requests, id)
	}
	return len(matched), nil
}

// AddRequest records an in-flight request on the shared ring. The host calls
// it when a submission is accepted. If req.ID is zero a unique ID is assigned
// and returned.
func (a *Authorizer) AddRequest(req Request) uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := a.stateFor(req.Tenant)
	if req.ID == 0 {
		a.nextID++
		req.ID = a.nextID
	} else if req.ID > a.nextID {
		a.nextID = req.ID
	}
	st.requests[req.ID] = req
	return req.ID
}

// RemoveRequest drops an in-flight request when it completes normally. It
// reports whether the request was present.
func (a *Authorizer) RemoveRequest(tenant TenantID, id uint64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	st, ok := a.tenants[tenant]
	if !ok {
		return false
	}
	if _, ok := st.requests[id]; !ok {
		return false
	}
	delete(st.requests, id)
	return true
}

// RegisterFixedFiles installs the host file descriptors backing a tenant's
// fixed file table slots. The host calls this after a successful
// IORING_REGISTER_FILES update for that tenant.
func (a *Authorizer) RegisterFixedFiles(tenant TenantID, slots map[int32]int32) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := a.stateFor(tenant)
	fixed := make(map[int32]int32, len(slots))
	for slot, fd := range slots {
		if slot < 0 {
			continue
		}
		fixed[slot] = fd
	}
	st.fixedFiles = fixed
}

// ReleaseFixedFiles clears a tenant's fixed file table, for example after
// IORING_UNREGISTER_FILES.
func (a *Authorizer) ReleaseFixedFiles(tenant TenantID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if st, ok := a.tenants[tenant]; ok {
		st.fixedFiles = make(map[int32]int32)
	}
}

// PendingCount returns the number of in-flight requests recorded for tenant.
// It is intended for host diagnostics and tests; each tenant can only inspect
// its own state.
func (a *Authorizer) PendingCount(tenant TenantID) int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	st, ok := a.tenants[tenant]
	if !ok {
		return 0
	}
	return len(st.requests)
}

// PendingRequests returns a snapshot of the in-flight requests for tenant.
func (a *Authorizer) PendingRequests(tenant TenantID) []Request {
	a.mu.RLock()
	defer a.mu.RUnlock()
	st, ok := a.tenants[tenant]
	if !ok {
		return nil
	}
	out := make([]Request, 0, len(st.requests))
	for _, req := range st.requests {
		out = append(out, req)
	}
	return out
}
