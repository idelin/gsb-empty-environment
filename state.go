package netsec

import (
	"fmt"
	"sort"
)

const (
	opCreateInode       = "create_inode"
	opPutEntry          = "put_entry"
	opDeleteEntry       = "delete_entry"
	opSwapEntries       = "swap_entries"
	opSetLinkCount      = "set_link_count"
	opSetOpenCount      = "set_open_count"
	opSetStatus         = "set_status"
	opSetPolicy         = "set_policy"
	opAddLease          = "add_lease"
	opCloseLease        = "close_lease"
	opExpireLease       = "expire_lease"
	opReleaseInode      = "release_inode"
	opCreateDirectory   = "create_directory"
	opSetDirPermissions = "set_directory_permissions"
	opReleaseDirectory  = "release_directory"
)

type txBeginPayload struct {
	Kind string `json:"kind"`
	Ops  int    `json:"ops"`
}

type txOpPayload struct {
	Index    int      `json:"index"`
	Mutation mutation `json:"mutation"`
}

type txCommitPayload struct {
	Ops int `json:"ops"`
}

type txRollbackPayload struct {
	Reason string `json:"reason"`
}

// mutation is the complete serialized effect of one operation. A transaction
// is applied only after every mutation and its commit record are authenticated
// and present in the log.
type mutation struct {
	Op string `json:"op"`

	InodeID          InodeID          `json:"inode_id,omitempty"`
	InodeGeneration  InodeGeneration  `json:"inode_generation,omitempty"`
	LinkCount        uint64           `json:"link_count,omitempty"`
	OpenCount        uint64           `json:"open_count,omitempty"`
	PolicyGeneration PolicyGeneration `json:"policy_generation,omitempty"`
	PolicyFlags      PolicyFlags      `json:"policy_flags,omitempty"`
	Status           InodeStatus      `json:"status,omitempty"`
	Data             []byte           `json:"data,omitempty"`
	NewGeneration    InodeGeneration  `json:"new_generation,omitempty"`

	DirectoryID            DirectoryID          `json:"directory_id,omitempty"`
	DirectoryGeneration    DirectoryGeneration  `json:"directory_generation,omitempty"`
	Permissions            DirectoryPermissions `json:"permissions,omitempty"`
	NewDirectoryGeneration DirectoryGeneration  `json:"new_directory_generation,omitempty"`

	Entry   *DirectoryEntry `json:"entry,omitempty"`
	Entry2  *DirectoryEntry `json:"entry2,omitempty"`
	Lease   *Lease          `json:"lease,omitempty"`
	LeaseID LeaseID         `json:"lease_id,omitempty"`
}

type inodeState struct {
	key       InodeKey
	linkCount uint64
	openCount uint64
	policyGen PolicyGeneration
	status    InodeStatus
	data      []byte
}

type directoryState struct {
	key         DirectoryKey
	permissions DirectoryPermissions
	entries     map[string]DirectoryEntry
}

type leaseState struct {
	lease  Lease
	closed bool
}

func (s *inodeState) snapshot() Inode {
	return Inode{
		ID:               s.key.ID,
		Generation:       s.key.Generation,
		LinkCount:        s.linkCount,
		OpenCount:        s.openCount,
		PolicyGeneration: s.policyGen,
		Status:           s.status,
	}
}

func (s *directoryState) snapshot() Directory {
	return Directory{
		ID:          s.key.ID,
		Generation:  s.key.Generation,
		Permissions: s.permissions,
	}
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	return append([]byte(nil), b...)
}

func (h *Host) inode(key InodeKey) (*inodeState, error) {
	s, ok := h.inodes[key]
	if !ok {
		if max := h.maxGeneration[key.ID]; max >= key.Generation {
			return nil, ErrInodeGenerationMismatch
		}
		return nil, ErrInodeNotFound
	}
	return s, nil
}

func (h *Host) directory(key DirectoryKey) (*directoryState, error) {
	s, ok := h.dirs[key]
	if !ok {
		if max := h.maxDirectoryGeneration[key.ID]; max >= key.Generation {
			return nil, ErrDirectoryGeneration
		}
		return nil, ErrDirectoryNotFound
	}
	return s, nil
}

func (h *Host) commit(kind string, mutations []mutation) (TransactionID, error) {
	if h.closed {
		return 0, ErrHostClosed
	}
	if len(mutations) == 0 {
		return 0, fmt.Errorf("%w: empty transaction", ErrInvalidOption)
	}
	tx := h.nextTxID
	h.nextTxID++

	specs := make([]recordSpec, 0, len(mutations)+2)
	specs = append(specs, recordSpec{
		tx:   tx,
		kind: recordBegin,
		payload: txBeginPayload{
			Kind: kind,
			Ops:  len(mutations),
		},
	})
	for i, m := range mutations {
		specs = append(specs, recordSpec{
			tx:   tx,
			kind: recordOp,
			payload: txOpPayload{
				Index:    i,
				Mutation: m,
			},
		})
	}
	specs = append(specs, recordSpec{
		tx:      tx,
		kind:    recordCommit,
		payload: txCommitPayload{Ops: len(mutations)},
	})

	if err := h.log.append(specs); err != nil {
		return 0, err
	}
	if err := applyMutations(h, mutations); err != nil {
		h.closed = true
		return 0, fmt.Errorf("%w: %v", ErrStateDiverged, err)
	}
	return tx, nil
}

func applyMutations(h *Host, mutations []mutation) error {
	for _, m := range mutations {
		if err := applyMutation(h, m); err != nil {
			return err
		}
	}
	return nil
}

func applyMutation(h *Host, m mutation) error {
	switch m.Op {
	case opCreateInode:
		key := InodeKey{ID: m.InodeID, Generation: m.InodeGeneration}
		if key.ID == 0 || key.Generation == 0 {
			return ErrStateDiverged
		}
		for active := range h.inodes {
			if active.ID == key.ID {
				return fmt.Errorf("%w: inode %d exists as generation %d", ErrStateDiverged, key.ID, active.Generation)
			}
		}
		if _, exists := h.inodes[key]; exists {
			h.noteDuplicateInode(key, "create_inode repeated")
			return nil
		}
		h.inodes[key] = &inodeState{
			key:       key,
			linkCount: m.LinkCount,
			openCount: m.OpenCount,
			policyGen: m.PolicyGeneration,
			status:    m.Status,
			data:      cloneBytes(m.Data),
		}
		if key.Generation > h.maxGeneration[key.ID] {
			h.maxGeneration[key.ID] = key.Generation
		}
		h.policies[key] = Policy{Generation: m.PolicyGeneration, Flags: m.PolicyFlags}
		if key.ID >= h.nextInodeID {
			h.nextInodeID = key.ID + 1
		}

	case opPutEntry:
		if m.Entry == nil {
			return ErrStateDiverged
		}
		if err := h.applyPutEntry(*m.Entry); err != nil {
			return err
		}

	case opDeleteEntry:
		if m.Entry == nil {
			return ErrStateDiverged
		}
		h.applyDeleteEntry(*m.Entry)

	case opSwapEntries:
		if m.Entry == nil || m.Entry2 == nil {
			return ErrStateDiverged
		}
		h.applySwapEntries(*m.Entry, *m.Entry2)

	case opSetLinkCount:
		s, err := h.inode(InodeKey{ID: m.InodeID, Generation: m.InodeGeneration})
		if err != nil {
			h.noteMissingInode(InodeKey{ID: m.InodeID, Generation: m.InodeGeneration})
			return nil
		}
		s.linkCount = m.LinkCount

	case opSetOpenCount:
		s, err := h.inode(InodeKey{ID: m.InodeID, Generation: m.InodeGeneration})
		if err != nil {
			h.noteMissingInode(InodeKey{ID: m.InodeID, Generation: m.InodeGeneration})
			return nil
		}
		s.openCount = m.OpenCount

	case opSetStatus:
		s, err := h.inode(InodeKey{ID: m.InodeID, Generation: m.InodeGeneration})
		if err != nil {
			h.noteMissingInode(InodeKey{ID: m.InodeID, Generation: m.InodeGeneration})
			return nil
		}
		s.status = m.Status

	case opSetPolicy:
		key := InodeKey{ID: m.InodeID, Generation: m.InodeGeneration}
		s, err := h.inode(key)
		if err != nil {
			h.noteMissingInode(key)
			return nil
		}
		s.policyGen = m.PolicyGeneration
		h.policies[key] = Policy{Generation: m.PolicyGeneration, Flags: m.PolicyFlags}

	case opAddLease:
		if m.Lease == nil || m.Lease.ID == 0 {
			return ErrStateDiverged
		}
		key := m.Lease.InodeKey()
		if _, err := h.inode(key); err != nil {
			h.noteMissingInode(key)
			return nil
		}
		if _, exists := h.leases[m.Lease.ID]; exists {
			h.noteSemantic("duplicate lease ID")
			return nil
		}
		lease := *m.Lease
		h.leases[lease.ID] = &leaseState{lease: lease}
		if lease.ID >= h.nextLeaseID {
			h.nextLeaseID = lease.ID + 1
		}

	case opCloseLease:
		ls, ok := h.leases[m.LeaseID]
		if !ok {
			h.noteSemantic("close of unknown lease")
			return nil
		}
		if ls.closed {
			h.noteSemantic("close of already closed lease")
			return nil
		}
		ls.closed = true

	case opExpireLease:
		ls, ok := h.leases[m.LeaseID]
		if !ok {
			h.noteSemantic("expiry of unknown lease")
			return nil
		}
		if m.Lease != nil {
			ls.lease.ExpiresAt = m.Lease.ExpiresAt
		}

	case opReleaseInode:
		key := InodeKey{ID: m.InodeID, Generation: m.InodeGeneration}
		s, ok := h.inodes[key]
		if !ok {
			h.noteMissingInode(key)
			return nil
		}
		if m.NewGeneration <= key.Generation {
			h.noteSemantic("release did not advance generation")
			return nil
		}
		if s.linkCount != 0 || s.openCount != 0 || s.status != InodeDeleting {
			return fmt.Errorf("%w: release precondition failed for %s", ErrStateDiverged, key)
		}
		if h.referenceCountLocked(key) != 0 {
			return fmt.Errorf("%w: release still has directory references for %s", ErrStateDiverged, key)
		}
		zeroBytes(s.data)
		delete(h.inodes, key)
		delete(h.policies, key)
		for id, lease := range h.leases {
			if lease.lease.InodeKey() == key {
				delete(h.leases, id)
			}
		}
		if m.NewGeneration > h.maxGeneration[key.ID] {
			h.maxGeneration[key.ID] = m.NewGeneration
		}

	case opCreateDirectory:
		key := DirectoryKey{ID: m.DirectoryID, Generation: m.DirectoryGeneration}
		if key.ID == 0 || key.Generation == 0 {
			return ErrStateDiverged
		}
		for active := range h.dirs {
			if active.ID == key.ID {
				return fmt.Errorf("%w: directory %d exists as generation %d", ErrStateDiverged, key.ID, active.Generation)
			}
		}
		if _, exists := h.dirs[key]; exists {
			h.noteSemantic("create_directory repeated")
			return nil
		}
		h.dirs[key] = &directoryState{
			key:         key,
			permissions: m.Permissions,
			entries:     make(map[string]DirectoryEntry),
		}
		if key.Generation > h.maxDirectoryGeneration[key.ID] {
			h.maxDirectoryGeneration[key.ID] = key.Generation
		}
		if key.ID >= h.nextDirectoryID {
			h.nextDirectoryID = key.ID + 1
		}

	case opSetDirPermissions:
		key := DirectoryKey{ID: m.DirectoryID, Generation: m.DirectoryGeneration}
		d, err := h.directory(key)
		if err != nil {
			h.noteSemantic("set permissions on missing directory")
			return nil
		}
		d.permissions = m.Permissions

	case opReleaseDirectory:
		key := DirectoryKey{ID: m.DirectoryID, Generation: m.DirectoryGeneration}
		d, ok := h.dirs[key]
		if !ok {
			h.noteSemantic("release of missing directory")
			return nil
		}
		if len(d.entries) != 0 {
			h.noteSemantic("release of non-empty directory")
			return nil
		}
		if m.NewDirectoryGeneration <= key.Generation {
			h.noteSemantic("directory release did not advance generation")
			return nil
		}
		delete(h.dirs, key)
		if m.NewDirectoryGeneration > h.maxDirectoryGeneration[key.ID] {
			h.maxDirectoryGeneration[key.ID] = m.NewDirectoryGeneration
		}

	default:
		return fmt.Errorf("%w: unknown mutation %q", ErrStateDiverged, m.Op)
	}
	return nil
}

func (h *Host) applyPutEntry(entry DirectoryEntry) error {
	d, err := h.directory(DirectoryKey{ID: entry.DirectoryID, Generation: entry.DirectoryGeneration})
	if err != nil {
		h.noteMissingDirectory(entry)
		return nil
	}
	if existing, ok := d.entries[entry.Name]; ok {
		h.noteDuplicateEntry(entry.Key(), fmt.Sprintf("name already binds inode %s", existing.InodeKey()))
		return nil
	}
	d.entries[entry.Name] = entry
	return nil
}

func (h *Host) applyDeleteEntry(entry DirectoryEntry) {
	d, err := h.directory(DirectoryKey{ID: entry.DirectoryID, Generation: entry.DirectoryGeneration})
	if err != nil {
		h.noteSemantic("delete from missing directory")
		return
	}
	existing, ok := d.entries[entry.Name]
	if !ok {
		h.noteSemantic("delete of missing directory entry")
		return
	}
	if existing.InodeKey() != entry.InodeKey() {
		h.noteSemantic("delete of directory entry with different inode identity")
		return
	}
	delete(d.entries, entry.Name)
}

func (h *Host) applySwapEntries(a, b DirectoryEntry) {
	da, errA := h.directory(DirectoryKey{ID: a.DirectoryID, Generation: a.DirectoryGeneration})
	db, errB := h.directory(DirectoryKey{ID: b.DirectoryID, Generation: b.DirectoryGeneration})
	if errA != nil || errB != nil {
		h.noteSemantic("swap involving missing directory")
		return
	}
	ea, okA := da.entries[a.Name]
	eb, okB := db.entries[b.Name]
	if !okA || !okB || ea.InodeKey() != a.InodeKey() || eb.InodeKey() != b.InodeKey() {
		h.noteSemantic("swap identities do not match current entries")
		return
	}
	delete(da.entries, a.Name)
	delete(db.entries, b.Name)
	atB := a
	atB.DirectoryID = b.DirectoryID
	atB.DirectoryGeneration = b.DirectoryGeneration
	atB.Name = b.Name
	atA := b
	atA.DirectoryID = a.DirectoryID
	atA.DirectoryGeneration = a.DirectoryGeneration
	atA.Name = a.Name
	db.entries[b.Name] = atB
	da.entries[a.Name] = atA
}

func (h *Host) noteDuplicateInode(key InodeKey, detail string) {
	h.quarantineInodes[key] = QuarantineRecord{
		Inode:  inodeKeyPtr(key),
		Reason: QuarantineLogSemanticMismatch,
		Detail: detail,
	}
}

func (h *Host) noteMissingInode(key InodeKey) {
	h.quarantineMissing = append(h.quarantineMissing, key)
}

func (h *Host) noteMissingDirectory(entry DirectoryEntry) {
	h.quarantineEntries[entry.Key()] = QuarantineRecord{
		Entry:  entryKeyPtr(entry.Key()),
		Reason: QuarantineLogSemanticMismatch,
		Detail: "put_entry targets a missing directory generation",
	}
}

func (h *Host) noteDuplicateEntry(key EntryKey, detail string) {
	h.duplicateEntries[key] = detail
	h.quarantineEntries[key] = QuarantineRecord{
		Entry:  entryKeyPtr(key),
		Reason: QuarantineDuplicateEntry,
		Detail: detail,
	}
}

func (h *Host) noteSemantic(detail string) {
	h.semanticProblems = append(h.semanticProblems, detail)
}

func (h *Host) referenceCountLocked(key InodeKey) int {
	n := 0
	for _, d := range h.dirs {
		for _, entry := range d.entries {
			if entry.InodeKey() == key {
				n++
			}
		}
	}
	return n
}

func inodeKeyPtr(k InodeKey) *InodeKey {
	return &k
}

func entryKeyPtr(k EntryKey) *EntryKey {
	return &k
}

func sortedInodeKeys(m map[InodeKey]*inodeState) []InodeKey {
	keys := make([]InodeKey, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].ID != keys[j].ID {
			return keys[i].ID < keys[j].ID
		}
		return keys[i].Generation < keys[j].Generation
	})
	return keys
}

func sortedDirectoryKeys(m map[DirectoryKey]*directoryState) []DirectoryKey {
	keys := make([]DirectoryKey, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].ID != keys[j].ID {
			return keys[i].ID < keys[j].ID
		}
		return keys[i].Generation < keys[j].Generation
	})
	return keys
}
