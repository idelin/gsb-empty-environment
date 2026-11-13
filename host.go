package netsec

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// CreateInodeOptions controls inode creation. ID 0 lets the host allocate a
// fresh inode number. A zero Policy.Generation installs generation 1 with
// PolicyAllowAll; a non-zero generation is used verbatim, including its flags.
type CreateInodeOptions struct {
	ID     InodeID
	Data   []byte
	Policy Policy
}

// Options configures a Host.
type Options struct {
	// LogPath is the authenticated append-only log. Empty means an in-memory
	// host that cannot survive process failure.
	LogPath string
	// AuthKey authenticates every log record and links records by a keyed
	// chain. At least 16 bytes are required.
	AuthKey []byte

	// LeaseRevoke is applied to leases that already exist when a policy is
	// revoked. The zero value is RevokeDrain.
	LeaseRevoke RevokePolicy
	// DisableAutoReclaim leaves Orphan/Deleting inodes for an explicit Sweep.
	// By default the host sweeps after Unlink, Close, and recovery.
	DisableAutoReclaim bool
	// Now supplies a clock for lease expiry. It defaults to time.Now.
	Now func() time.Time
	// ClearData is called immediately before OnReclaim. It should overwrite or
	// clear any external inode payload. The in-memory payload is already
	// zeroed. The callback must be idempotent because a crash before the
	// release record can cause it to be retried during recovery. A fresh full
	// directory scan precedes this callback. Do not call Sweep or Shutdown
	// from this callback.
	ClearData func(InodeKey) error
	// OnReclaim runs only after a fresh full-directory scan has proven zero
	// references. It receives the immutable Inode snapshot and that proof.
	// Do not call Sweep or Shutdown from this callback.
	OnReclaim func(ReclaimEvent) error
}

// Host is a crash-recoverable inode and directory host.
type Host struct {
	opts Options
	log  *authLog

	mu        sync.RWMutex
	reclaimMu sync.Mutex

	inodes   map[InodeKey]*inodeState
	dirs     map[DirectoryKey]*directoryState
	policies map[InodeKey]Policy
	leases   map[LeaseID]*leaseState

	maxGeneration          map[InodeID]InodeGeneration
	maxDirectoryGeneration map[DirectoryID]DirectoryGeneration

	nextInodeID     InodeID
	nextDirectoryID DirectoryID
	nextLeaseID     LeaseID
	nextTxID        TransactionID

	quarantineInodes  map[InodeKey]QuarantineRecord
	quarantineEntries map[EntryKey]QuarantineRecord
	duplicateEntries  map[EntryKey]string
	quarantineMissing []InodeKey
	semanticProblems  []string
	recovery          RecoveryReport
	closed            bool
}

// NewHost opens Options.LogPath, authenticates the log, rolls back incomplete
// transactions, audits every inode against an independent directory scan, and
// optionally reclaims proven-unreferenced orphans.
func NewHost(opts Options) (*Host, error) {
	if len(opts.AuthKey) < 16 {
		return nil, ErrMissingAuthKey
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.LeaseRevoke.Mode == 0 {
		opts.LeaseRevoke.Mode = RevokeDrain
	}
	if opts.LeaseRevoke.Mode == RevokeGrace && opts.LeaseRevoke.GracePeriod < 0 {
		return nil, ErrInvalidOption
	}

	log, truncated, err := newAuthLog(opts.LogPath, opts.AuthKey)
	if err != nil {
		return nil, err
	}
	h := &Host{
		opts:                   opts,
		log:                    log,
		inodes:                 make(map[InodeKey]*inodeState),
		dirs:                   make(map[DirectoryKey]*directoryState),
		policies:               make(map[InodeKey]Policy),
		leases:                 make(map[LeaseID]*leaseState),
		maxGeneration:          make(map[InodeID]InodeGeneration),
		maxDirectoryGeneration: make(map[DirectoryID]DirectoryGeneration),
		nextInodeID:            1,
		nextDirectoryID:        1,
		nextLeaseID:            1,
		nextTxID:               1,
		quarantineInodes:       make(map[InodeKey]QuarantineRecord),
		quarantineEntries:      make(map[EntryKey]QuarantineRecord),
		duplicateEntries:       make(map[EntryKey]string),
	}
	if err := h.replay(log.records); err != nil {
		_ = log.close()
		return nil, err
	}
	h.recovery.Records = len(log.records)
	h.recovery.TruncatedBytes = truncated
	h.auditReferences()
	h.fillRecoveryQuarantine()

	if !opts.DisableAutoReclaim {
		_, sweepErr := h.Sweep()
		if sweepErr != nil {
			_ = h.Shutdown()
			return nil, sweepErr
		}
	}
	return h, nil
}

// Close with no argument, or Shutdown, syncs and closes the authenticated log.
// Close(lease) closes one open lease instead. The combined entry point lets
// both host shutdown and the conventional lease operation be called Close.
func (h *Host) Close(arg ...any) error {
	if len(arg) == 0 {
		return h.Shutdown()
	}
	if len(arg) != 1 {
		return ErrInvalidOption
	}
	if arg[0] == nil {
		return ErrLeaseNotFound
	}
	switch v := arg[0].(type) {
	case *Lease:
		return h.closeLease(v)
	case Lease:
		return h.closeLease(&v)
	default:
		return ErrInvalidOption
	}
}

// Shutdown syncs and closes the authenticated log. It does not delete inodes
// or close leases.
func (h *Host) Shutdown() error {
	h.reclaimMu.Lock()
	defer h.reclaimMu.Unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return h.log.close()
	}
	h.closed = true
	return h.log.close()
}

// LogBytes returns the authenticated log for diagnostics and crash tests.
func (h *Host) LogBytes() []byte {
	return h.log.raw()
}

// RecoveryReport returns a copy of the report produced by the latest open and
// subsequent automatic sweeps.
func (h *Host) RecoveryReport() RecoveryReport {
	h.mu.RLock()
	defer h.mu.RUnlock()
	r := h.recovery
	r.RolledBack = append([]TransactionID(nil), r.RolledBack...)
	r.QuarantinedInodes = append([]QuarantineRecord(nil), r.QuarantinedInodes...)
	r.QuarantinedEntries = append([]QuarantineRecord(nil), r.QuarantinedEntries...)
	r.SemanticProblems = append([]string(nil), r.SemanticProblems...)
	r.Reclaimed = append([]InodeKey(nil), r.Reclaimed...)
	return r
}

// CreateDirectory creates a directory with a fresh directory ID.
func (h *Host) CreateDirectory(permissions DirectoryPermissions) (DirectoryKey, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.nextDirectoryID
	return h.createDirectoryLocked(id, permissions)
}

// CreateDirectoryWithID creates or recreates a directory ID. A reused ID gets
// the next generation, so stale entries for the old incarnation cannot bind to
// it.
func (h *Host) CreateDirectoryWithID(id DirectoryID, permissions DirectoryPermissions) (DirectoryKey, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if id == 0 {
		id = h.nextDirectoryID
	}
	return h.createDirectoryLocked(id, permissions)
}

func (h *Host) createDirectoryLocked(id DirectoryID, permissions DirectoryPermissions) (DirectoryKey, error) {
	if h.closed {
		return DirectoryKey{}, ErrHostClosed
	}
	for key := range h.dirs {
		if key.ID == id {
			return DirectoryKey{}, fmt.Errorf("%w: directory ID is still active in generation %d", ErrDirectoryGeneration, key.Generation)
		}
	}
	gen := h.maxDirectoryGeneration[id] + 1
	if gen == 0 {
		return DirectoryKey{}, ErrDirectoryGeneration
	}
	key := DirectoryKey{ID: id, Generation: gen}
	if _, exists := h.dirs[key]; exists {
		return DirectoryKey{}, fmt.Errorf("%w: directory generation already exists", ErrDirectoryGeneration)
	}
	_, err := h.commit("create_directory", []mutation{{
		Op:                  opCreateDirectory,
		DirectoryID:         key.ID,
		DirectoryGeneration: key.Generation,
		Permissions:         permissions,
	}})
	if err != nil {
		return DirectoryKey{}, err
	}
	if key.ID >= h.nextDirectoryID {
		h.nextDirectoryID = key.ID + 1
	}
	return key, nil
}

// SetDirectoryPermissions atomically changes the permissions of an exact
// directory generation.
func (h *Host) SetDirectoryPermissions(key DirectoryKey, permissions DirectoryPermissions) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return ErrHostClosed
	}
	if _, err := h.directory(key); err != nil {
		return err
	}
	_, err := h.commit("set_directory_permissions", []mutation{{
		Op:                  opSetDirPermissions,
		DirectoryID:         key.ID,
		DirectoryGeneration: key.Generation,
		Permissions:         permissions,
	}})
	return err
}

// ReleaseDirectory releases an empty directory and advances its generation.
func (h *Host) ReleaseDirectory(key DirectoryKey) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return ErrHostClosed
	}
	d, err := h.directory(key)
	if err != nil {
		return err
	}
	if len(d.entries) != 0 {
		return ErrDirectoryNotEmpty
	}
	next := key.Generation + 1
	if next == 0 {
		return ErrDirectoryGeneration
	}
	_, err = h.commit("release_directory", []mutation{{
		Op:                     opReleaseDirectory,
		DirectoryID:            key.ID,
		DirectoryGeneration:    key.Generation,
		NewDirectoryGeneration: next,
	}})
	return err
}

// CreateInode creates an unreferenced Orphan inode. Callers normally Link it
// immediately; an explicit Sweep may reclaim it while it has no names or open
// leases.
func (h *Host) CreateInode(opts CreateInodeOptions) (InodeKey, error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return InodeKey{}, ErrHostClosed
	}
	id := opts.ID
	if id == 0 {
		id = h.nextInodeID
	}
	for key := range h.inodes {
		if key.ID == id {
			h.mu.Unlock()
			return InodeKey{}, fmt.Errorf("%w: inode ID is still active in generation %d", ErrInodeGenerationMismatch, key.Generation)
		}
	}
	gen := h.maxGeneration[id] + 1
	if gen == 0 {
		h.mu.Unlock()
		return InodeKey{}, ErrInodeGenerationMismatch
	}
	key := InodeKey{ID: id, Generation: gen}
	if _, exists := h.inodes[key]; exists {
		h.mu.Unlock()
		return InodeKey{}, fmt.Errorf("%w: inode generation already exists", ErrInodeGenerationMismatch)
	}
	policy := opts.Policy
	if policy.Generation == 0 {
		policy.Generation = 1
		policy.Flags = PolicyAllowAll
	}
	_, err := h.commit("create_inode", []mutation{{
		Op:               opCreateInode,
		InodeID:          key.ID,
		InodeGeneration:  key.Generation,
		LinkCount:        0,
		OpenCount:        0,
		PolicyGeneration: policy.Generation,
		PolicyFlags:      policy.Flags,
		Status:           InodeOrphan,
		Data:             cloneBytes(opts.Data),
	}})
	if key.ID >= h.nextInodeID {
		h.nextInodeID = key.ID + 1
	}
	h.mu.Unlock()
	if err != nil {
		return InodeKey{}, err
	}
	return key, nil
}

// GetInode returns an inode snapshot for an exact incarnation.
func (h *Host) GetInode(key InodeKey) (Inode, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	s, ok := h.inodes[key]
	if !ok {
		return Inode{}, false
	}
	return s.snapshot(), true
}

// ListInodes returns sorted inode snapshots.
func (h *Host) ListInodes() []Inode {
	h.mu.RLock()
	defer h.mu.RUnlock()
	keys := sortedInodeKeys(h.inodes)
	out := make([]Inode, 0, len(keys))
	for _, key := range keys {
		out = append(out, h.inodes[key].snapshot())
	}
	return out
}

// ReadInode returns a copy of the inode payload. Data is retained for orphaned
// inodes until every open lease has closed.
func (h *Host) ReadInode(key InodeKey) ([]byte, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	s, err := h.inode(key)
	if err != nil {
		return nil, err
	}
	return cloneBytes(s.data), nil
}

// GetDirectory returns a directory snapshot.
func (h *Host) GetDirectory(key DirectoryKey) (Directory, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	d, ok := h.dirs[key]
	if !ok {
		return Directory{}, false
	}
	return d.snapshot(), true
}

// ListDirectories returns sorted directory snapshots.
func (h *Host) ListDirectories() []Directory {
	h.mu.RLock()
	defer h.mu.RUnlock()
	keys := sortedDirectoryKeys(h.dirs)
	out := make([]Directory, 0, len(keys))
	for _, key := range keys {
		out = append(out, h.dirs[key].snapshot())
	}
	return out
}

// Lookup returns the exact directory entry bound to dir/name.
func (h *Host) Lookup(dir DirectoryKey, name string) (DirectoryEntry, error) {
	canonical, err := CanonicalName(name)
	if err != nil {
		return DirectoryEntry{}, err
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	d, err := h.directory(dir)
	if err != nil {
		return DirectoryEntry{}, err
	}
	entry, ok := d.entries[canonical]
	if !ok {
		return DirectoryEntry{}, ErrEntryNotFound
	}
	return entry, nil
}

// ScanReferences performs a fresh complete scan over the directory tree. It
// deliberately does not use the orphan list, lease state, or any cached
// reference count.
func (h *Host) ScanReferences() []DirectoryEntry {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.scanReferencesLocked()
}

func (h *Host) scanReferencesLocked() []DirectoryEntry {
	out := make([]DirectoryEntry, 0)
	for _, dkey := range sortedDirectoryKeys(h.dirs) {
		d := h.dirs[dkey]
		for _, entry := range d.entries {
			out = append(out, entry)
		}
	}
	sortedEntries(out)
	return out
}

func (h *Host) referenceProof(inode InodeKey) ReferenceProof {
	all := h.scanReferencesLocked()
	proof := ReferenceProof{
		DirectoriesScanned: len(h.dirs),
		EntriesScanned:     len(all),
		Matching:           make([]DirectoryEntry, 0),
	}
	for _, entry := range all {
		if entry.InodeKey() == inode {
			proof.Matching = append(proof.Matching, entry)
		}
	}
	return proof
}

// QuarantinedInodes returns recovery-isolated inodes. Their link counts are
// left unchanged and they are never automatically reclaimed.
func (h *Host) QuarantinedInodes() []QuarantineRecord {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]QuarantineRecord, 0, len(h.quarantineInodes))
	for _, rec := range h.quarantineInodes {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Inode == nil || out[j].Inode == nil {
			return out[i].Detail < out[j].Detail
		}
		if out[i].Inode.ID != out[j].Inode.ID {
			return out[i].Inode.ID < out[j].Inode.ID
		}
		return out[i].Inode.Generation < out[j].Inode.Generation
	})
	return out
}

// QuarantinedEntries returns isolated directory-name keys.
func (h *Host) QuarantinedEntries() []QuarantineRecord {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]QuarantineRecord, 0, len(h.quarantineEntries))
	for _, rec := range h.quarantineEntries {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Entry == nil || out[j].Entry == nil {
			return out[i].Detail < out[j].Detail
		}
		a, b := out[i].Entry, out[j].Entry
		if a.DirectoryID != b.DirectoryID {
			return a.DirectoryID < b.DirectoryID
		}
		if a.DirectoryGeneration != b.DirectoryGeneration {
			return a.DirectoryGeneration < b.DirectoryGeneration
		}
		return a.Name < b.Name
	})
	return out
}
