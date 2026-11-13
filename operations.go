package netsec

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// Link adds a hard link to an exact inode incarnation. The optional expected
// policy generation makes policy validation race-explicit. Both the source's
// PolicyAllowLink flag and the target directory's PermCreate are checked before
// the directory entry and link count are committed in one transaction.
func (h *Host) Link(inode InodeKey, target DirectoryKey, name string, expectedPolicy ...PolicyGeneration) error {
	canonical, err := CanonicalName(name)
	if err != nil {
		return err
	}
	if len(expectedPolicy) > 1 {
		return ErrInvalidOption
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return ErrHostClosed
	}
	s, err := h.inode(inode)
	if err != nil {
		h.mu.Unlock()
		return err
	}
	if _, bad := h.quarantineInodes[inode]; bad {
		h.mu.Unlock()
		return ErrInodeQuarantined
	}
	if s.status == InodeDeleting {
		h.mu.Unlock()
		return ErrInodeNotLive
	}
	policy, ok := h.policies[inode]
	if !ok || policy.Generation != s.policyGen {
		h.mu.Unlock()
		return ErrPolicyGenerationMismatch
	}
	if len(expectedPolicy) == 1 && expectedPolicy[0] != policy.Generation {
		h.mu.Unlock()
		return ErrPolicyGenerationMismatch
	}
	if policy.Flags&PolicyAllowLink == 0 {
		h.mu.Unlock()
		return ErrPolicyDenied
	}
	d, err := h.directory(target)
	if err != nil {
		h.mu.Unlock()
		return err
	}
	if d.permissions&PermCreate == 0 {
		h.mu.Unlock()
		return ErrPermissionDenied
	}
	if _, exists := d.entries[canonical]; exists {
		h.mu.Unlock()
		return ErrEntryExists
	}
	if s.linkCount == math.MaxUint64 {
		h.mu.Unlock()
		return ErrLinkCountUnderflow
	}

	entry := DirectoryEntry{
		DirectoryID:         target.ID,
		DirectoryGeneration: target.Generation,
		Name:                canonical,
		InodeID:             inode.ID,
		InodeGeneration:     inode.Generation,
	}
	newCount := s.linkCount + 1
	_, err = h.commit("link", []mutation{
		{Op: opPutEntry, Entry: &entry},
		{
			Op:              opSetLinkCount,
			InodeID:         inode.ID,
			InodeGeneration: inode.Generation,
			LinkCount:       newCount,
		},
		{
			Op:              opSetStatus,
			InodeID:         inode.ID,
			InodeGeneration: inode.Generation,
			Status:          InodeLive,
		},
	})
	h.mu.Unlock()
	return err
}

// LinkWithPolicy is the explicit form of Link's optional policy-generation
// argument.
func (h *Host) LinkWithPolicy(inode InodeKey, policyGeneration PolicyGeneration, target DirectoryKey, name string) error {
	return h.Link(inode, target, name, policyGeneration)
}

// Unlink removes precisely the supplied directory entry. A stale generation or
// a name that now binds another inode never causes a decrement.
func (h *Host) Unlink(entry DirectoryEntry) error {
	canonical, err := CanonicalName(entry.Name)
	if err != nil {
		return err
	}
	entry.Name = canonical

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return ErrHostClosed
	}
	dirKey := DirectoryKey{ID: entry.DirectoryID, Generation: entry.DirectoryGeneration}
	d, err := h.directory(dirKey)
	if err != nil {
		h.mu.Unlock()
		return err
	}
	if d.permissions&PermRemove == 0 {
		h.mu.Unlock()
		return ErrPermissionDenied
	}
	current, ok := d.entries[canonical]
	if !ok {
		h.mu.Unlock()
		return ErrEntryNotFound
	}
	if current.InodeID != entry.InodeID || current.InodeGeneration != entry.InodeGeneration {
		h.mu.Unlock()
		return ErrEntryIdentityMismatch
	}
	s, err := h.inode(entry.InodeKey())
	if err != nil {
		h.mu.Unlock()
		return err
	}
	if s.linkCount == 0 {
		h.mu.Unlock()
		return ErrLinkCountUnderflow
	}
	newCount := s.linkCount - 1
	newStatus := InodeLive
	if newCount == 0 {
		newStatus = InodeOrphan
	}
	_, err = h.commit("unlink", []mutation{
		{Op: opDeleteEntry, Entry: &entry},
		{
			Op:              opSetLinkCount,
			InodeID:         entry.InodeID,
			InodeGeneration: entry.InodeGeneration,
			LinkCount:       newCount,
		},
		{
			Op:              opSetStatus,
			InodeID:         entry.InodeID,
			InodeGeneration: entry.InodeGeneration,
			Status:          newStatus,
		},
	})
	h.mu.Unlock()
	if err != nil {
		return err
	}
	if newCount == 0 && !h.opts.DisableAutoReclaim {
		if _, sweepErr := h.Sweep(); sweepErr != nil {
			return sweepErr
		}
	}
	return nil
}

// Rename moves one exact directory entry. The old delete and new insert are in
// the same transaction, so recovery cannot observe a half-rename.
func (h *Host) Rename(from DirectoryEntry, to DirectoryKey, toName string) error {
	fromName, err := CanonicalName(from.Name)
	if err != nil {
		return err
	}
	toCanonical, err := CanonicalName(toName)
	if err != nil {
		return err
	}
	from.Name = fromName

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return ErrHostClosed
	}
	fromKey := DirectoryKey{ID: from.DirectoryID, Generation: from.DirectoryGeneration}
	fromDir, err := h.directory(fromKey)
	if err != nil {
		return err
	}
	toDir, err := h.directory(to)
	if err != nil {
		return err
	}
	if fromDir.permissions&PermRemove == 0 || toDir.permissions&PermCreate == 0 {
		return ErrPermissionDenied
	}
	current, ok := fromDir.entries[from.Name]
	if !ok {
		return ErrEntryNotFound
	}
	if current.InodeKey() != from.InodeKey() {
		return ErrEntryIdentityMismatch
	}
	if fromKey == to && from.Name == toCanonical {
		return nil
	}
	if _, exists := toDir.entries[toCanonical]; exists {
		return ErrEntryExists
	}
	newEntry := DirectoryEntry{
		DirectoryID:         to.ID,
		DirectoryGeneration: to.Generation,
		Name:                toCanonical,
		InodeID:             from.InodeID,
		InodeGeneration:     from.InodeGeneration,
	}
	_, err = h.commit("rename", []mutation{
		{Op: opDeleteEntry, Entry: &from},
		{Op: opPutEntry, Entry: &newEntry},
	})
	return err
}

// Exchange atomically swaps the targets of two exact directory entries. It is
// suitable for rename-exchange and never changes either inode's link count.
func (h *Host) Exchange(a, b DirectoryEntry) error {
	aName, err := CanonicalName(a.Name)
	if err != nil {
		return err
	}
	bName, err := CanonicalName(b.Name)
	if err != nil {
		return err
	}
	a.Name, b.Name = aName, bName
	if a.Key() == b.Key() {
		return nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return ErrHostClosed
	}
	da, err := h.directory(DirectoryKey{ID: a.DirectoryID, Generation: a.DirectoryGeneration})
	if err != nil {
		return err
	}
	db, err := h.directory(DirectoryKey{ID: b.DirectoryID, Generation: b.DirectoryGeneration})
	if err != nil {
		return err
	}
	if da.permissions&PermRemove == 0 || da.permissions&PermCreate == 0 ||
		db.permissions&PermRemove == 0 || db.permissions&PermCreate == 0 {
		return ErrPermissionDenied
	}
	ea, okA := da.entries[a.Name]
	eb, okB := db.entries[b.Name]
	if !okA || !okB {
		return ErrEntryNotFound
	}
	if ea.InodeKey() != a.InodeKey() || eb.InodeKey() != b.InodeKey() {
		return ErrEntryIdentityMismatch
	}
	_, err = h.commit("exchange", []mutation{{Op: opSwapEntries, Entry: &a, Entry2: &b}})
	return err
}

// RenameExchange is an alias for Exchange.
func (h *Host) RenameExchange(a, b DirectoryEntry) error {
	return h.Exchange(a, b)
}

// Open takes a generation-bound lease for an exact directory entry. Policy
// revocation blocks this operation immediately. The optional expected policy
// generation protects callers that cached an older policy.
func (h *Host) Open(entry DirectoryEntry, expectedPolicy ...PolicyGeneration) (*Lease, error) {
	canonical, err := CanonicalName(entry.Name)
	if err != nil {
		return nil, err
	}
	if len(expectedPolicy) > 1 {
		return nil, ErrInvalidOption
	}
	entry.Name = canonical

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, ErrHostClosed
	}
	d, err := h.directory(DirectoryKey{ID: entry.DirectoryID, Generation: entry.DirectoryGeneration})
	if err != nil {
		return nil, err
	}
	if d.permissions&PermLookup == 0 {
		return nil, ErrPermissionDenied
	}
	current, ok := d.entries[canonical]
	if !ok {
		return nil, ErrEntryNotFound
	}
	if current.InodeKey() != entry.InodeKey() {
		return nil, ErrEntryIdentityMismatch
	}
	if _, bad := h.quarantineEntries[current.Key()]; bad {
		return nil, ErrEntryQuarantined
	}
	key := entry.InodeKey()
	if _, bad := h.quarantineInodes[key]; bad {
		return nil, ErrInodeQuarantined
	}
	s, err := h.inode(key)
	if err != nil {
		return nil, err
	}
	if s.status != InodeLive {
		return nil, ErrInodeNotLive
	}
	policy, ok := h.policies[key]
	if !ok || policy.Generation != s.policyGen {
		return nil, ErrPolicyGenerationMismatch
	}
	if len(expectedPolicy) == 1 && expectedPolicy[0] != policy.Generation {
		return nil, ErrPolicyGenerationMismatch
	}
	if policy.Flags&PolicyAllowOpen == 0 {
		return nil, ErrPolicyDenied
	}
	if s.openCount == math.MaxUint64 {
		return nil, ErrInodeBusy
	}
	leaseID := h.nextLeaseID
	h.nextLeaseID++
	now := h.opts.Now()
	lease := Lease{
		ID:               leaseID,
		InodeID:          key.ID,
		InodeGeneration:  key.Generation,
		PolicyGeneration: policy.Generation,
		OpenedAt:         now,
	}
	_, err = h.commit("open", []mutation{
		{
			Op:              opSetOpenCount,
			InodeID:         key.ID,
			InodeGeneration: key.Generation,
			OpenCount:       s.openCount + 1,
		},
		{Op: opAddLease, Lease: &lease},
	})
	if err != nil {
		return nil, err
	}
	return &lease, nil
}

// OpenWithPolicy is the explicit form of Open's optional policy-generation
// argument.
func (h *Host) OpenWithPolicy(entry DirectoryEntry, policyGeneration PolicyGeneration) (*Lease, error) {
	return h.Open(entry, policyGeneration)
}

// closeLease closes one lease exactly once. If this was the last lease on an
// orphaned inode, the Deleting transition and release are in the same Close
// path before the lease is returned.
func (h *Host) closeLease(lease *Lease) error {
	if lease == nil {
		return ErrLeaseNotFound
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return ErrHostClosed
	}
	ls, ok := h.leases[lease.ID]
	if !ok {
		h.mu.Unlock()
		return ErrLeaseNotFound
	}
	if ls.closed {
		h.mu.Unlock()
		return ErrLeaseClosed
	}
	if ls.lease.InodeKey() != lease.InodeKey() {
		h.mu.Unlock()
		return ErrInodeGenerationMismatch
	}
	if ls.lease.PolicyGeneration != lease.PolicyGeneration {
		h.mu.Unlock()
		return ErrLeasePolicyMismatch
	}
	key := lease.InodeKey()
	s, err := h.inode(key)
	if err != nil {
		h.mu.Unlock()
		return err
	}
	if s.openCount == 0 {
		h.mu.Unlock()
		return ErrStateDiverged
	}
	newOpen := s.openCount - 1
	newStatus := s.status
	if s.linkCount == 0 {
		if newOpen == 0 {
			newStatus = InodeDeleting
		} else {
			newStatus = InodeOrphan
		}
	}
	_, err = h.commit("close", []mutation{
		{Op: opCloseLease, LeaseID: lease.ID},
		{
			Op:              opSetOpenCount,
			InodeID:         key.ID,
			InodeGeneration: key.Generation,
			OpenCount:       newOpen,
		},
		{
			Op:              opSetStatus,
			InodeID:         key.ID,
			InodeGeneration: key.Generation,
			Status:          newStatus,
		},
	})
	h.mu.Unlock()
	if err != nil {
		return err
	}
	if newStatus == InodeDeleting && !h.opts.DisableAutoReclaim {
		if _, sweepErr := h.Sweep(); sweepErr != nil {
			return sweepErr
		}
	}
	return nil
}

// CloseLease closes one lease exactly once.
func (h *Host) CloseLease(lease *Lease) error {
	return h.closeLease(lease)
}

// ExpireLeases closes active leases whose configured grace period has elapsed.
// It returns the IDs that were closed.
func (h *Host) ExpireLeases(now time.Time) ([]LeaseID, error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, ErrHostClosed
	}
	ids, err := h.expireLeasesLocked(now)
	h.mu.Unlock()
	if err != nil {
		return ids, err
	}
	if !h.opts.DisableAutoReclaim {
		if _, sweepErr := h.Sweep(); sweepErr != nil {
			return ids, sweepErr
		}
	}
	return ids, nil
}

func (h *Host) expireLeasesLocked(now time.Time) ([]LeaseID, error) {
	var expired []Lease
	for _, ls := range h.leases {
		if ls.closed || ls.lease.ExpiresAt.IsZero() || now.Before(ls.lease.ExpiresAt) {
			continue
		}
		expired = append(expired, ls.lease)
	}
	sort.Slice(expired, func(i, j int) bool {
		return expired[i].ID < expired[j].ID
	})
	ids := make([]LeaseID, 0, len(expired))
	for i := range expired {
		if err := h.closeLocked(&expired[i], InodeDeleting); err != nil {
			return ids, err
		}
		ids = append(ids, expired[i].ID)
	}
	return ids, nil
}

func (h *Host) closeLocked(lease *Lease, orphanTerminal InodeStatus) error {
	ls, ok := h.leases[lease.ID]
	if !ok {
		return ErrLeaseNotFound
	}
	if ls.closed {
		return ErrLeaseClosed
	}
	if ls.lease.InodeKey() != lease.InodeKey() || ls.lease.PolicyGeneration != lease.PolicyGeneration {
		return ErrLeasePolicyMismatch
	}
	key := lease.InodeKey()
	s, err := h.inode(key)
	if err != nil {
		return err
	}
	if s.openCount == 0 {
		return ErrStateDiverged
	}
	newOpen := s.openCount - 1
	newStatus := s.status
	if s.linkCount == 0 {
		if newOpen == 0 {
			newStatus = orphanTerminal
		} else {
			newStatus = InodeOrphan
		}
	}
	_, err = h.commit("expire_lease", []mutation{
		{Op: opCloseLease, LeaseID: lease.ID},
		{
			Op:              opSetOpenCount,
			InodeID:         key.ID,
			InodeGeneration: key.Generation,
			OpenCount:       newOpen,
		},
		{
			Op:              opSetStatus,
			InodeID:         key.ID,
			InodeGeneration: key.Generation,
			Status:          newStatus,
		},
	})
	return err
}

// RevokePolicy immediately denies new opens and links, then applies the
// configured handling to leases that already exist.
func (h *Host) RevokePolicy(inode InodeKey, opts RevokeOptions) (PolicyGeneration, error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return 0, ErrHostClosed
	}
	s, err := h.inode(inode)
	if err != nil {
		h.mu.Unlock()
		return 0, err
	}
	if _, bad := h.quarantineInodes[inode]; bad {
		h.mu.Unlock()
		return 0, ErrInodeQuarantined
	}
	mode := opts.Mode
	if mode == 0 {
		mode = h.opts.LeaseRevoke.Mode
	}
	grace := opts.GracePeriod
	if grace == 0 {
		grace = h.opts.LeaseRevoke.GracePeriod
	}
	if mode != RevokeDrain && mode != RevokeGrace && mode != RevokeEnd {
		h.mu.Unlock()
		return 0, ErrInvalidOption
	}
	if mode == RevokeGrace && grace < 0 {
		h.mu.Unlock()
		return 0, ErrInvalidOption
	}
	nextGen := s.policyGen + 1
	if nextGen == 0 {
		h.mu.Unlock()
		return 0, ErrPolicyGenerationMismatch
	}

	mutations := []mutation{{
		Op:               opSetPolicy,
		InodeID:          inode.ID,
		InodeGeneration:  inode.Generation,
		PolicyGeneration: nextGen,
		PolicyFlags:      0,
	}}
	newOpen := s.openCount
	newStatus := s.status
	var closeIDs []LeaseID
	for _, ls := range h.leases {
		if ls.closed || ls.lease.InodeKey() != inode {
			continue
		}
		switch mode {
		case RevokeGrace:
			lease := ls.lease
			lease.ExpiresAt = h.opts.Now().Add(grace)
			mutations = append(mutations, mutation{Op: opExpireLease, LeaseID: ls.lease.ID, Lease: &lease})
		case RevokeEnd:
			mutations = append(mutations, mutation{Op: opCloseLease, LeaseID: ls.lease.ID})
			closeIDs = append(closeIDs, ls.lease.ID)
			if newOpen > 0 {
				newOpen--
			}
		}
	}
	if mode == RevokeEnd && newOpen != s.openCount {
		mutations = append(mutations, mutation{
			Op:              opSetOpenCount,
			InodeID:         inode.ID,
			InodeGeneration: inode.Generation,
			OpenCount:       newOpen,
		})
		if s.linkCount == 0 && newOpen == 0 {
			newStatus = InodeDeleting
			mutations = append(mutations, mutation{
				Op:              opSetStatus,
				InodeID:         inode.ID,
				InodeGeneration: inode.Generation,
				Status:          newStatus,
			})
		}
	}
	_, err = h.commit("revoke_policy", mutations)
	h.mu.Unlock()
	if err != nil {
		return 0, err
	}
	if len(closeIDs) > 0 && newStatus == InodeDeleting && !h.opts.DisableAutoReclaim {
		if _, sweepErr := h.Sweep(); sweepErr != nil {
			return nextGen, sweepErr
		}
	}
	return nextGen, nil
}

// SetInodePolicy installs a new policy revision. expectedGeneration must match
// the current inode policy generation. A zero policy generation advances the
// generation by one.
func (h *Host) SetInodePolicy(inode InodeKey, expectedGeneration PolicyGeneration, policy Policy) (PolicyGeneration, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return 0, ErrHostClosed
	}
	s, err := h.inode(inode)
	if err != nil {
		return 0, err
	}
	if s.policyGen != expectedGeneration {
		return 0, ErrPolicyGenerationMismatch
	}
	if policy.Generation == 0 {
		policy.Generation = expectedGeneration + 1
		if policy.Generation == 0 {
			policy.Generation = 1
		}
	}
	if policy.Generation <= expectedGeneration {
		return 0, fmt.Errorf("%w: policy generation must advance", ErrPolicyGenerationMismatch)
	}
	_, err = h.commit("set_policy", []mutation{{
		Op:               opSetPolicy,
		InodeID:          inode.ID,
		InodeGeneration:  inode.Generation,
		PolicyGeneration: policy.Generation,
		PolicyFlags:      policy.Flags,
	}})
	if err != nil {
		return 0, err
	}
	return policy.Generation, nil
}
