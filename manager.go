package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type region struct {
	id          string
	size        int64
	state       RegionState
	credential  [32]byte
	tenant      string
	allocatedAt time.Time
	stripes     int64
	verified    map[int64]bool
}

// Manager owns the durable region state, the erase journal and the volatile
// authentication lockouts.
type Manager struct {
	mu sync.Mutex

	cfg        Config
	journal    *journal
	device     Device
	ownsDevice bool
	faults     Faults

	regions    map[string]*region
	failedAuth map[string]int
	locked     map[string]bool

	closed bool
}

// NewManager opens a manager rooted at dir.  Options override the safe
// defaults.  Recovery is explicit by default; use WithAutoRecover to resume
// erasing regions during construction.
func NewManager(dir string, opts ...Option) (*Manager, error) {
	cfg := DefaultConfig(dir)
	for _, opt := range opts {
		opt(&cfg)
	}
	return NewManagerWithConfig(cfg)
}

// NewManagerWithConfig opens a manager from an explicit Config.  Zero values
// for MaxAuthFailures and SyncMode receive safe defaults.
func NewManagerWithConfig(cfg Config) (*Manager, error) {
	if cfg.MaxAuthFailures <= 0 {
		cfg.MaxAuthFailures = DefaultMaxAuthFailures
	}
	if cfg.Dir != "" && cfg.JournalPath == "" {
		cfg.JournalPath = filepath.Join(cfg.Dir, "erase-journal.jsonl")
	}
	if cfg.JournalPath == "" {
		return nil, fmt.Errorf("%w: no journal path or directory", ErrCorruptJournal)
	}
	cfg.CredentialKey = append([]byte(nil), cfg.CredentialKey...)

	m := &Manager{
		cfg:        cfg,
		regions:    make(map[string]*region),
		failedAuth: make(map[string]int),
		locked:     make(map[string]bool),
	}
	if cfg.Faults != nil {
		m.faults = *cfg.Faults
	}

	if cfg.Device == nil {
		if cfg.Dir == "" {
			return nil, fmt.Errorf("%w: no device or directory", ErrInvalidRegion)
		}
		dev, err := NewFileDevice(filepath.Join(cfg.Dir, "regions"), cfg.SyncMode)
		if err != nil {
			return nil, err
		}
		m.device = dev
		m.ownsDevice = true
	} else {
		m.device = cfg.Device
	}

	j, entries, err := openJournal(cfg.JournalPath)
	if err != nil {
		m.closeDevice()
		return nil, err
	}
	m.journal = j
	for _, e := range entries {
		if err := m.applyEntry(e); err != nil {
			j.close()
			m.closeDevice()
			return nil, err
		}
	}
	for id, r := range m.regions {
		if err := m.device.Prepare(id, r.size); err != nil {
			j.close()
			m.closeDevice()
			return nil, fmt.Errorf("prepare region %q: %w", id, err)
		}
	}
	if cfg.AutoRecover {
		if err := m.recoverLocked(); err != nil {
			j.close()
			m.closeDevice()
			return nil, err
		}
	}
	return m, nil
}

// CreateRegion provisions a region and immediately sanitizes it.  The region
// is journaled as erasing first, so a power cut during provisioning leaves it
// non-allocatable and recoverable rather than falsely clean.
func (m *Manager) CreateRegion(id string, size int64, credential Credential) error {
	if err := validateRegionID(id); err != nil {
		return err
	}
	if err := validateRegionSize(size); err != nil {
		return err
	}
	if credential == "" {
		return fmt.Errorf("%w: empty credential", ErrInvalidCredential)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.usable(); err != nil {
		return err
	}
	if _, ok := m.regions[id]; ok {
		return fmt.Errorf("%w: %q", ErrRegionExists, id)
	}
	if err := m.device.Prepare(id, size); err != nil {
		return fmt.Errorf("prepare region %q: %w", id, err)
	}

	r := &region{
		id:         id,
		size:       size,
		state:      StateErasing,
		credential: m.hashCredential(id, credential),
		stripes:    size / StripeSize,
		verified:   make(map[int64]bool),
	}
	entry := journalEntry{
		Kind:           kindCreate,
		Region:         id,
		Size:           size,
		CredentialHash: hex.EncodeToString(r.credential[:]),
		StripeCount:    r.stripes,
	}
	if err := m.journal.append(entry); err != nil {
		return err
	}
	m.regions[id] = r
	return m.eraseRegion(r)
}

// Create is an alias for CreateRegion.
func (m *Manager) Create(id string, size int64, credential Credential) error {
	return m.CreateRegion(id, size, credential)
}

// Allocate hands a clean region to a tenant.  An optional tenant string may be
// supplied; it is stored for lease matching and diagnostics.
func (m *Manager) Allocate(id string, tenant ...string) (*Lease, error) {
	if len(tenant) > 1 {
		return nil, fmt.Errorf("%w: at most one tenant", ErrInvalidRegion)
	}
	name := ""
	if len(tenant) == 1 {
		name = tenant[0]
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.usable(); err != nil {
		return nil, err
	}
	r, ok := m.regions[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrRegionNotFound, id)
	}
	if m.locked[id] {
		return nil, fmt.Errorf("%w: region %q (%d consecutive failures)", ErrRegionLocked, id, m.failedAuth[id])
	}
	if !r.state.Allocatable() {
		return nil, fmt.Errorf("%w: region %q is %s", ErrNotAllocatable, id, r.state)
	}

	now := time.Now().UTC()
	entry := journalEntry{Kind: kindAllocate, Region: id, Tenant: name, Time: now.UnixNano()}
	if err := m.journal.append(entry); err != nil {
		return nil, err
	}
	r.state = StateInUse
	r.tenant = name
	r.allocatedAt = now
	return &Lease{RegionID: id, Tenant: name, AllocatedAt: now}, nil
}

// AllocateRegion is an alias for Allocate.
func (m *Manager) AllocateRegion(id string, tenant ...string) (*Lease, error) {
	return m.Allocate(id, tenant...)
}

// ReleaseLease releases an allocated region and performs the full authorized
// erase.  The release fails closed if the lease's tenant no longer matches.
func (m *Manager) ReleaseLease(lease *Lease, credential Credential) error {
	if lease == nil {
		return fmt.Errorf("%w: nil lease", ErrLeaseMismatch)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.usable(); err != nil {
		return err
	}
	r, ok := m.regions[lease.RegionID]
	if !ok {
		return fmt.Errorf("%w: %q", ErrRegionNotFound, lease.RegionID)
	}
	if (r.state != StateInUse && r.state != StateErasing) || r.tenant != lease.Tenant {
		return fmt.Errorf("%w: region %q", ErrLeaseMismatch, lease.RegionID)
	}
	if !r.allocatedAt.Equal(lease.AllocatedAt) {
		return fmt.Errorf("%w: stale lease for region %q", ErrLeaseMismatch, lease.RegionID)
	}
	return m.releaseLocked(r, credential)
}

// Release authenticates the credential, durably records the start of erasing,
// and synchronously overwrites every stripe.  Calling Release again on an
// interrupted erasing region re-authenticates and resumes it.  On any failure
// the region remains erasing and can also be resumed after restart with Recover.
func (m *Manager) Release(id string, credential Credential) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.usable(); err != nil {
		return err
	}
	r, ok := m.regions[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrRegionNotFound, id)
	}
	return m.releaseLocked(r, credential)
}

func (m *Manager) releaseLocked(r *region, credential Credential) error {
	switch r.state {
	case StateInUse:
		if err := m.authorize(r, credential); err != nil {
			return err
		}
		if err := m.beginEraseLocked(r); err != nil {
			return err
		}
	case StateErasing:
		if err := m.authorize(r, credential); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: region %q is %s", ErrNotInUse, r.id, r.state)
	}
	return m.eraseRegion(r)
}

// BeginErase authenticates and durably moves an in-use region to erasing
// without doing I/O.  A later Erase, ResumeErase or Recover performs the
// stripes.
func (m *Manager) BeginErase(id string, credential Credential) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.usable(); err != nil {
		return err
	}
	r, ok := m.regions[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrRegionNotFound, id)
	}
	if r.state != StateInUse {
		return fmt.Errorf("%w: region %q is %s", ErrNotInUse, r.id, r.state)
	}
	if err := m.authorize(r, credential); err != nil {
		return err
	}
	return m.beginEraseLocked(r)
}

// Erase authenticates and runs the erase.  It is idempotent for an already
// clean region.  For a region already erasing it re-authenticates before
// continuing, and a lockout therefore also blocks manual continuation.
func (m *Manager) Erase(id string, credential Credential) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.usable(); err != nil {
		return err
	}
	r, ok := m.regions[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrRegionNotFound, id)
	}
	switch r.state {
	case StateClean:
		// Idempotent no-op, but still require the region credential so a
		// caller cannot mistake Erase for an unauthenticated entry point.
		if err := m.authorize(r, credential); err != nil {
			return err
		}
		return nil
	case StateInUse:
		if err := m.authorize(r, credential); err != nil {
			return err
		}
		if err := m.beginEraseLocked(r); err != nil {
			return err
		}
	case StateErasing:
		if err := m.authorize(r, credential); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: region %q is %s", ErrNotErasing, id, r.state)
	}
	return m.eraseRegion(r)
}

// ResumeErase continues an already-authorized erase without asking for the
// credential again.  It is intended for service startup and is what Recover
// uses internally.
func (m *Manager) ResumeErase(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.usable(); err != nil {
		return err
	}
	r, ok := m.regions[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrRegionNotFound, id)
	}
	if r.state != StateErasing {
		return fmt.Errorf("%w: region %q is %s", ErrNotErasing, id, r.state)
	}
	return m.eraseRegion(r)
}

// Recover resumes every region left in the erasing state.  A simulated power
// loss stops recovery immediately, leaving that region erasing for the next
// start.  Other per-region I/O errors are joined after attempting the rest.
func (m *Manager) Recover() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.usable(); err != nil {
		return err
	}
	return m.recoverLocked()
}

func (m *Manager) recoverLocked() error {
	ids := m.regionIDs()
	var errs []error
	for _, id := range ids {
		r := m.regions[id]
		if r.state != StateErasing {
			continue
		}
		if m.faults.BeforeRecover != nil {
			if err := m.faults.BeforeRecover(id); err != nil {
				if errors.Is(err, ErrPowerLoss) {
					return err
				}
				errs = append(errs, err)
				continue
			}
		}
		if err := m.eraseRegion(r); err != nil {
			if errors.Is(err, ErrPowerLoss) {
				return err
			}
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Authorize checks a credential and updates the volatile consecutive-failure
// counter without changing region state.  It is useful to lease controllers
// that need an authentication check before another operation.
func (m *Manager) Authorize(id string, credential Credential) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.usable(); err != nil {
		return err
	}
	r, ok := m.regions[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrRegionNotFound, id)
	}
	return m.authorize(r, credential)
}

// SetFaults installs or replaces deterministic fault hooks.  It is normally
// used by tests after provisioning so that a simulated cut applies only to the
// operation under test.
func (m *Manager) SetFaults(f Faults) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.faults = f
}

// ClearFaults removes all fault hooks.
func (m *Manager) ClearFaults() {
	m.SetFaults(Faults{})
}

// MaxAuthFailures returns the configured consecutive-failure lockout limit.
func (m *Manager) MaxAuthFailures() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg.MaxAuthFailures
}

func (m *Manager) authorize(r *region, credential Credential) error {
	if m.locked[r.id] {
		return fmt.Errorf("%w: region %q (%d consecutive failures)", ErrRegionLocked, r.id, m.failedAuth[r.id])
	}
	got := m.hashCredential(r.id, credential)
	if subtle.ConstantTimeCompare(got[:], r.credential[:]) != 1 {
		m.failedAuth[r.id]++
		if m.failedAuth[r.id] >= m.cfg.MaxAuthFailures {
			m.locked[r.id] = true
			return fmt.Errorf("%w: %w: region %q after %d consecutive failures",
				ErrAuthenticationFailed, ErrRegionLocked, r.id, m.failedAuth[r.id])
		}
		return fmt.Errorf("%w: region %q (%d/%d)", ErrAuthenticationFailed, r.id, m.failedAuth[r.id], m.cfg.MaxAuthFailures)
	}
	m.failedAuth[r.id] = 0
	return nil
}

func (m *Manager) hashCredential(id, credential string) [32]byte {
	var h hash.Hash
	if len(m.cfg.CredentialKey) > 0 {
		h = hmac.New(sha256.New, m.cfg.CredentialKey)
	} else {
		h = sha256.New()
	}
	h.Write([]byte("netsec-region-credential"))
	h.Write([]byte{0})
	h.Write([]byte(id))
	h.Write([]byte{0})
	h.Write([]byte(credential))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func (m *Manager) beginEraseLocked(r *region) error {
	entry := journalEntry{
		Kind:        kindBeginErase,
		Region:      r.id,
		StripeCount: r.stripes,
	}
	if err := m.journal.append(entry); err != nil {
		return err
	}
	r.state = StateErasing
	r.verified = make(map[int64]bool)
	return nil
}

// Status returns a snapshot including the volatile lockout controls.
func (m *Manager) Status(id string) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.usable(); err != nil {
		return Status{}, err
	}
	r, ok := m.regions[id]
	if !ok {
		return Status{}, fmt.Errorf("%w: %q", ErrRegionNotFound, id)
	}
	return m.statusLocked(r), nil
}

// State returns just the lifecycle state.
func (m *Manager) State(id string) (RegionState, error) {
	st, err := m.Status(id)
	if err != nil {
		return 0, err
	}
	return st.State, nil
}

// IsLocked reports whether consecutive authentication failures have frozen
// authorization for the region until restart.
func (m *Manager) IsLocked(id string) (bool, error) {
	st, err := m.Status(id)
	if err != nil {
		return false, err
	}
	return st.Locked, nil
}

// Frozen is an alias for IsLocked, emphasizing that authorization for the
// region is frozen until restart.
func (m *Manager) Frozen(id string) (bool, error) {
	return m.IsLocked(id)
}

// Regions returns snapshots of every region in ID order.
func (m *Manager) Regions() ([]Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.usable(); err != nil {
		return nil, err
	}
	out := make([]Status, 0, len(m.regions))
	for _, id := range m.regionIDs() {
		out = append(out, m.statusLocked(m.regions[id]))
	}
	return out, nil
}

func (m *Manager) statusLocked(r *region) Status {
	return Status{
		RegionID:           r.id,
		Size:               r.size,
		State:              r.state,
		Tenant:             r.tenant,
		AllocatedAt:        r.allocatedAt,
		TotalStripes:       r.stripes,
		VerifiedStripes:    int64(len(r.verified)),
		Locked:             m.locked[r.id],
		FailedAuthAttempts: m.failedAuth[r.id],
	}
}

func (m *Manager) regionIDs() []string {
	ids := make([]string, 0, len(m.regions))
	for id := range m.regions {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (m *Manager) usable() error {
	if m.closed {
		return ErrClosed
	}
	if m.journal == nil || m.journal.broken != nil {
		return fmt.Errorf("%w: journal unavailable", ErrClosed)
	}
	return nil
}

// Close flushes and closes the journal and any device owned by the manager.
// Callers that supplied their own Device retain ownership of it.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	var errs []error
	if m.journal != nil {
		if err := m.journal.close(); err != nil {
			errs = append(errs, err)
		}
	}
	if m.ownsDevice {
		if closer, ok := m.device.(io.Closer); ok {
			if err := closer.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) closeDevice() {
	if m.ownsDevice {
		if closer, ok := m.device.(io.Closer); ok {
			_ = closer.Close()
		}
	}
}

func (m *Manager) applyEntry(e journalEntry) error {
	if err := validateRegionID(e.Region); err != nil {
		return fmt.Errorf("%w: %w", ErrCorruptJournal, err)
	}
	switch e.Kind {
	case kindCreate:
		if _, ok := m.regions[e.Region]; ok {
			return fmt.Errorf("%w: duplicate create for %q", ErrCorruptJournal, e.Region)
		}
		if err := validateRegionSize(e.Size); err != nil {
			return fmt.Errorf("%w: %w", ErrCorruptJournal, err)
		}
		hashBytes, err := hex.DecodeString(e.CredentialHash)
		if err != nil || len(hashBytes) != 32 {
			return fmt.Errorf("%w: invalid credential hash for %q", ErrCorruptJournal, e.Region)
		}
		r := &region{
			id:       e.Region,
			size:     e.Size,
			state:    StateErasing,
			stripes:  e.Size / StripeSize,
			verified: make(map[int64]bool),
		}
		copy(r.credential[:], hashBytes)
		if e.StripeCount != 0 && e.StripeCount != r.stripes {
			return fmt.Errorf("%w: stripe count mismatch for %q", ErrCorruptJournal, e.Region)
		}
		m.regions[e.Region] = r
	case kindAllocate:
		r, ok := m.regions[e.Region]
		if !ok {
			return fmt.Errorf("%w: allocate unknown region %q", ErrCorruptJournal, e.Region)
		}
		if r.state != StateClean {
			return fmt.Errorf("%w: allocate region %q in state %s", ErrCorruptJournal, e.Region, r.state)
		}
		r.state = StateInUse
		r.tenant = e.Tenant
		r.allocatedAt = time.Unix(0, e.Time).UTC()
	case kindBeginErase:
		r, ok := m.regions[e.Region]
		if !ok {
			return fmt.Errorf("%w: begin erase unknown region %q", ErrCorruptJournal, e.Region)
		}
		if r.state != StateInUse {
			return fmt.Errorf("%w: begin erase region %q in state %s", ErrCorruptJournal, e.Region, r.state)
		}
		if e.StripeCount != 0 && e.StripeCount != r.stripes {
			return fmt.Errorf("%w: stripe count mismatch for %q", ErrCorruptJournal, e.Region)
		}
		r.state = StateErasing
		r.verified = make(map[int64]bool)
	case kindStripeChecked:
		r, ok := m.regions[e.Region]
		if !ok {
			return fmt.Errorf("%w: stripe for unknown region %q", ErrCorruptJournal, e.Region)
		}
		if r.state != StateErasing {
			return fmt.Errorf("%w: stripe for region %q in state %s", ErrCorruptJournal, e.Region, r.state)
		}
		if e.Stripe < 0 || e.Stripe >= r.stripes {
			return fmt.Errorf("%w: stripe %d outside region %q", ErrCorruptJournal, e.Stripe, e.Region)
		}
		r.verified[e.Stripe] = true
	case kindClean:
		r, ok := m.regions[e.Region]
		if !ok {
			return fmt.Errorf("%w: clean unknown region %q", ErrCorruptJournal, e.Region)
		}
		if r.state != StateErasing {
			return fmt.Errorf("%w: clean region %q in state %s", ErrCorruptJournal, e.Region, r.state)
		}
		if int64(len(r.verified)) != r.stripes {
			return fmt.Errorf("%w: clean region %q with %d/%d verified stripes", ErrCorruptJournal, e.Region, len(r.verified), r.stripes)
		}
		r.state = StateClean
		r.tenant = ""
		r.allocatedAt = time.Time{}
		r.verified = nil
	default:
		return fmt.Errorf("%w: unknown journal record %q", ErrCorruptJournal, e.Kind)
	}
	return nil
}

var _ io.Closer = (*FileDevice)(nil)
