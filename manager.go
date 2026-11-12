package netsec

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// RevokeReason explains why a previously assigned group was torn down at
// runtime.
type RevokeReason int

const (
	// RevokeHotplug means a new device appeared in the group after assign.
	RevokeHotplug RevokeReason = iota
	// RevokeHotRemove means a member device disappeared from the group.
	RevokeHotRemove
	// RevokeRebind means a member device's driver binding changed.
	RevokeRebind
	// RevokeIntegrity means a re-inspection found a host/unbound driver.
	RevokeIntegrity
	// RevokeGroupGone means the IOMMU group itself vanished from sysfs.
	RevokeGroupGone
	// RevokeClosed means the manager was shut down.
	RevokeClosed
	// RevokeReleased means the group was detached through an orderly Release.
	RevokeReleased
)

func (r RevokeReason) String() string {
	switch r {
	case RevokeHotplug:
		return "new device hot-plugged into group"
	case RevokeHotRemove:
		return "member device hot-removed from group"
	case RevokeRebind:
		return "member device re-bound to another driver"
	case RevokeIntegrity:
		return "group no longer satisfies driver integrity"
	case RevokeGroupGone:
		return "iommu group disappeared"
	case RevokeClosed:
		return "manager closed"
	case RevokeReleased:
		return "group released"
	default:
		return "unknown"
	}
}

// Revocation records a single runtime teardown of a leased group.
type Revocation struct {
	GroupID  int
	TenantID string
	Reason   RevokeReason
	Detail   string
}

// RevokeHook is invoked after a group is revoked while a VM is running. It is
// the proxy's cue to detach the affected VFIO group from the container and
// quarantine the tenant. Hooks run on a separate goroutine and must not block.
type RevokeHook func(Revocation)

// Sentinel errors returned by the manager. Callers can use errors.Is to match.
var (
	// ErrTenantConflict means a different tenant already owns the group.
	ErrTenantConflict = errors.New("iommu group is assigned to another tenant")
	// ErrConcurrentChange means the group changed during the attach window.
	ErrConcurrentChange = errors.New("iommu group changed during attachment")
	// ErrManagerClosed means the manager has been closed.
	ErrManagerClosed = errors.New("manager is closed")
	// ErrUnknownGroup means Release was called for a group that is not leased.
	ErrUnknownGroup = errors.New("iommu group is not currently attached")
)

// Option configures a Manager.
type Option func(*Manager)

// WithAllowedDrivers adds driver names (beyond the built-in VFIO/stub set)
// that are permitted to own a group member.
func WithAllowedDrivers(names ...string) Option {
	return func(m *Manager) { m.allowed = allowedDriverSet(names) }
}

// WithPolling forces the portable polling watcher with the given interval,
// even on Linux. Primarily used by tests.
func WithPolling(interval time.Duration) Option {
	return func(m *Manager) {
		m.forcePoll = true
		m.pollInterval = interval
	}
}

// WithWatcherFactory injects a custom Watcher constructor. The factory is
// called once per attached group.
func WithWatcherFactory(f func() (Watcher, error)) Option {
	return func(m *Manager) { m.watcherFactory = f }
}

// WithRevokeHook registers a callback fired on every runtime revocation.
func WithRevokeHook(h RevokeHook) Option {
	return func(m *Manager) { m.hook = h }
}

// Lease is a successful assignment of one IOMMU group to one tenant's
// container. It becomes invalid the moment the group's integrity is broken.
type Lease struct {
	GroupID  int
	TenantID string

	revocation chan Revocation
	once       sync.Once
}

// Revoked returns a channel that receives the Revocation when the lease is
// torn down and is then closed.
func (l *Lease) Revoked() <-chan Revocation { return l.revocation }

// IsActive reports whether the lease is still valid.
func (l *Lease) IsActive() bool {
	select {
	case <-l.revocation:
		return false
	default:
		return true
	}
}

// attachedGroup holds the runtime state of one leased group.
type attachedGroup struct {
	groupID  int
	tenantID string
	lease    *Lease
	watcher  Watcher
	baseline GroupInfo
	stop     chan struct{}
	done     chan struct{}
	shutdown sync.Once
}

// groupDirResolver is implemented by Sysfs backends that can locate a group's
// devices directory on disk, which the watcher needs.
type groupDirResolver interface {
	GroupDevicesDir(groupID int) string
}

// Manager enforces VFIO IOMMU group integrity for a passthrough proxy. It is
// safe for concurrent use.
type Manager struct {
	sys     Sysfs
	mu      sync.Mutex
	allowed map[string]struct{}

	forcePoll    bool
	pollInterval time.Duration

	watcherFactory func() (Watcher, error)
	hook           RevokeHook

	groups map[int]*attachedGroup
	closed bool
}

// NewManager creates a Manager backed by sys.
func NewManager(sys Sysfs, opts ...Option) *Manager {
	m := &Manager{
		sys:     sys,
		allowed: allowedDriverSet(nil),
		groups:  make(map[int]*attachedGroup),
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// ValidateGroup runs the pre-start integrity check for a group without
// attaching it. It returns the inspected snapshot on success, an
// *IntegrityError / ErrGroupEmpty when the group is unsafe, or a *SysfsError
// when sysfs cannot be read.
func (m *Manager) ValidateGroup(groupID int) (GroupInfo, error) {
	info, err := m.sys.InspectGroup(groupID)
	if err != nil {
		return GroupInfo{}, err
	}
	if err := CheckGroup(info, m.allowed); err != nil {
		return info, err
	}
	return info, nil
}

// Attach validates groupID and, if safe, leases it to tenantID's container and
// starts runtime monitoring. It enforces all assignment rules:
//
//   - every member must be on a VFIO/stub driver (else *IntegrityError);
//   - a group is held by at most one lease/container at a time;
//   - a group already owned by another tenant is refused (ErrTenantConflict);
//   - cross-tenant sharing of one group is therefore always rejected.
//
// Re-attaching an already active lease to the same tenant returns the
// existing lease instead of creating a duplicate.
func (m *Manager) Attach(groupID int, tenantID string) (*Lease, error) {
	if tenantID == "" {
		return nil, errors.New("tenant id must not be empty")
	}

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrManagerClosed
	}
	if existing, ok := m.groups[groupID]; ok {
		if existing.tenantID != tenantID {
			m.mu.Unlock()
			return nil, fmt.Errorf("%w: group %d owned by %q, requested by %q",
				ErrTenantConflict, groupID, existing.tenantID, tenantID)
		}
		lease := existing.lease
		m.mu.Unlock()
		return lease, nil
	}

	// First integrity check: reject host-driver members and empty groups
	// before any watcher is set up.
	info, err := m.sys.InspectGroup(groupID)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if err := CheckGroup(info, m.allowed); err != nil {
		m.mu.Unlock()
		return nil, err
	}

	watcher, err := m.newWatcher()
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}

	ag := &attachedGroup{
		groupID:  groupID,
		tenantID: tenantID,
		lease: &Lease{
			GroupID:    groupID,
			TenantID:   tenantID,
			revocation: make(chan Revocation, 1),
		},
		watcher:  watcher,
		baseline: info,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}

	// Register watched directories before re-checking so a change made
	// between the two inspections cannot slip through unnoticed.
	if err := m.watchGroup(ag, info); err != nil {
		_ = watcher.Close()
		m.mu.Unlock()
		return nil, err
	}

	// Second inspection closes the TOCTOU window: membership and driver
	// bindings must be identical to what the first check approved.
	current, err := m.sys.InspectGroup(groupID)
	if err != nil {
		_ = watcher.Close()
		m.mu.Unlock()
		return nil, err
	}
	if !sameMembership(info, current) {
		_ = watcher.Close()
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: group %d changed between check and attach",
			ErrConcurrentChange, groupID)
	}

	m.groups[groupID] = ag
	m.mu.Unlock()

	go m.watchLoop(ag)
	return ag.lease, nil
}

// Release detaches a group from its container and stops monitoring. It is the
// orderly teardown path used when a VM shuts down normally. Releasing an
// already released group returns ErrUnknownGroup.
func (m *Manager) Release(groupID int) error {
	m.mu.Lock()
	ag, ok := m.groups[groupID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("%w: %d", ErrUnknownGroup, groupID)
	}
	delete(m.groups, groupID)
	m.mu.Unlock()

	m.revoke(ag, Revocation{
		GroupID:  ag.groupID,
		TenantID: ag.tenantID,
		Reason:   RevokeReleased,
		Detail:   "orderly release",
	})
	m.teardown(ag)
	return nil
}

// IsAttached reports whether groupID currently has an active lease.
func (m *Manager) IsAttached(groupID int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.groups[groupID]
	return ok
}

// TenantOf returns the tenant that owns groupID, or "" if it is not attached.
func (m *Manager) TenantOf(groupID int) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ag, ok := m.groups[groupID]; ok {
		return ag.tenantID
	}
	return ""
}

// Close revokes every active lease (with RevokeClosed) and releases all
// watchers. It is idempotent.
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	leased := make([]*attachedGroup, 0, len(m.groups))
	for _, ag := range m.groups {
		leased = append(leased, ag)
	}
	m.groups = make(map[int]*attachedGroup)
	m.mu.Unlock()

	for _, ag := range leased {
		m.revoke(ag, Revocation{
			GroupID:  ag.groupID,
			TenantID: ag.tenantID,
			Reason:   RevokeClosed,
			Detail:   "manager shutting down",
		})
		m.teardown(ag)
	}
	return nil
}

func (m *Manager) newWatcher() (Watcher, error) {
	if m.watcherFactory != nil {
		return m.watcherFactory()
	}
	return newDefaultWatcher(m.forcePoll, m.pollInterval)
}
