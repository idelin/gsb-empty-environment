package netsec

import (
	"fmt"
)

// Sweep reclaims every inode that has linkCount=0, openCount=0, no directory
// reference in a fresh full scan, and no quarantine. It first commits the
// Deleting state, then invokes callbacks outside all host locks, and only then
// commits the release and generation increment.
func (h *Host) Sweep() ([]InodeKey, error) {
	h.reclaimMu.Lock()
	defer h.reclaimMu.Unlock()

	var reclaimed []InodeKey
	for {
		h.mu.Lock()
		if h.closed {
			h.mu.Unlock()
			return reclaimed, ErrHostClosed
		}
		if _, err := h.expireLeasesLocked(h.opts.Now()); err != nil {
			h.mu.Unlock()
			return reclaimed, err
		}
		key, ok := h.nextReclaimCandidateLocked()
		h.mu.Unlock()
		if !ok {
			return reclaimed, nil
		}
		did, err := h.reclaimCandidate(key)
		if err != nil {
			return reclaimed, err
		}
		if did {
			reclaimed = append(reclaimed, key)
			h.mu.Lock()
			h.recovery.Reclaimed = append(h.recovery.Reclaimed, key)
			h.mu.Unlock()
		}
	}
}

func (h *Host) nextReclaimCandidateLocked() (InodeKey, bool) {
	for _, key := range sortedInodeKeys(h.inodes) {
		s := h.inodes[key]
		if _, bad := h.quarantineInodes[key]; bad {
			continue
		}
		if s.linkCount != 0 || s.openCount != 0 {
			continue
		}
		if s.status != InodeOrphan && s.status != InodeDeleting {
			continue
		}
		if proof := h.referenceProof(key); len(proof.Matching) != 0 {
			h.quarantineInodes[key] = QuarantineRecord{
				Inode:  inodeKeyPtr(key),
				Reason: QuarantineLinkCountMismatch,
				Detail: fmt.Sprintf("orphan candidate still has %d directory references", len(proof.Matching)),
			}
			continue
		}
		return key, true
	}
	return InodeKey{}, false
}

func (h *Host) reclaimCandidate(key InodeKey) (bool, error) {
	h.mu.Lock()
	s, ok := h.inodes[key]
	if !ok {
		h.mu.Unlock()
		return false, nil
	}
	if _, bad := h.quarantineInodes[key]; bad ||
		s.linkCount != 0 || s.openCount != 0 ||
		(s.status != InodeOrphan && s.status != InodeDeleting) {
		h.mu.Unlock()
		return false, nil
	}
	proof := h.referenceProof(key)
	if len(proof.Matching) != 0 {
		h.quarantineInodes[key] = QuarantineRecord{
			Inode:  inodeKeyPtr(key),
			Reason: QuarantineLinkCountMismatch,
			Detail: fmt.Sprintf("orphan candidate still has %d directory references", len(proof.Matching)),
		}
		h.mu.Unlock()
		return false, nil
	}
	if s.status == InodeOrphan {
		if _, err := h.commit("reclaim_prepare", []mutation{{
			Op:              opSetStatus,
			InodeID:         key.ID,
			InodeGeneration: key.Generation,
			Status:          InodeDeleting,
		}}); err != nil {
			h.mu.Unlock()
			return false, err
		}
	}
	s = h.inodes[key]
	snapshot := s.snapshot()
	zeroBytes(s.data)
	s.data = nil
	h.mu.Unlock()

	// Rebuild the proof from a fresh complete directory scan immediately before
	// each externally visible callback. The scan intentionally ignores
	// linkCount, lease state, and the orphan list.
	proof = h.referenceProofPublic(key)
	if len(proof.Matching) != 0 {
		h.mu.Lock()
		h.quarantineInodes[key] = QuarantineRecord{
			Inode:  inodeKeyPtr(key),
			Reason: QuarantineLinkCountMismatch,
			Detail: fmt.Sprintf("reference appeared before reclaim callback: %d", len(proof.Matching)),
		}
		h.mu.Unlock()
		return false, ErrLinkCountMismatch
	}
	if h.opts.ClearData != nil {
		if err := h.opts.ClearData(key); err != nil {
			return false, fmt.Errorf("%w: clear data: %v", ErrReclaimCallback, err)
		}
	}
	if h.opts.OnReclaim != nil {
		proof = h.referenceProofPublic(key)
		if len(proof.Matching) != 0 {
			h.mu.Lock()
			h.quarantineInodes[key] = QuarantineRecord{
				Inode:  inodeKeyPtr(key),
				Reason: QuarantineLinkCountMismatch,
				Detail: fmt.Sprintf("reference appeared before OnReclaim: %d", len(proof.Matching)),
			}
			h.mu.Unlock()
			return false, ErrLinkCountMismatch
		}
		event := ReclaimEvent{
			Inode:       snapshot,
			Proof:       proof,
			ReclaimedAt: h.opts.Now(),
		}
		if err := h.opts.OnReclaim(event); err != nil {
			return false, fmt.Errorf("%w: %v", ErrReclaimCallback, err)
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok = h.inodes[key]
	if !ok {
		return false, nil
	}
	if _, bad := h.quarantineInodes[key]; bad {
		return false, nil
	}
	if s.linkCount != 0 || s.openCount != 0 || s.status != InodeDeleting {
		return false, nil
	}
	proof = h.referenceProof(key)
	if len(proof.Matching) != 0 {
		h.quarantineInodes[key] = QuarantineRecord{
			Inode:  inodeKeyPtr(key),
			Reason: QuarantineLinkCountMismatch,
			Detail: fmt.Sprintf("reference appeared before release: %d", len(proof.Matching)),
		}
		return false, nil
	}
	newGeneration := key.Generation + 1
	if newGeneration == 0 {
		return false, ErrInodeGenerationMismatch
	}
	_, err := h.commit("reclaim_release", []mutation{{
		Op:              opReleaseInode,
		InodeID:         key.ID,
		InodeGeneration: key.Generation,
		NewGeneration:   newGeneration,
	}})
	if err != nil {
		return false, err
	}
	return true, nil
}

func (h *Host) referenceProofPublic(key InodeKey) ReferenceProof {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.referenceProof(key)
}
