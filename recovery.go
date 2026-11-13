package netsec

import (
	"encoding/json"
	"fmt"
	"sort"
)

type replayedTxn struct {
	kind      string
	ops       int
	mutations map[int]mutation
}

func (h *Host) replay(records []decodedRecord) error {
	pending := make(map[TransactionID]*replayedTxn)
	var maxTx TransactionID

	for _, rec := range records {
		if rec.tx > maxTx {
			maxTx = rec.tx
		}
		switch rec.kind {
		case recordBegin:
			var payload txBeginPayload
			if err := json.Unmarshal(rec.payload, &payload); err != nil {
				return fmt.Errorf("%w: invalid begin payload", ErrLogCorrupt)
			}
			if rec.tx == 0 || payload.Kind == "" || payload.Ops <= 0 {
				return fmt.Errorf("%w: invalid begin record", ErrLogCorrupt)
			}
			if _, exists := pending[rec.tx]; exists {
				return fmt.Errorf("%w: duplicate begin for transaction %d", ErrLogCorrupt, rec.tx)
			}
			pending[rec.tx] = &replayedTxn{
				kind:      payload.Kind,
				ops:       payload.Ops,
				mutations: make(map[int]mutation, payload.Ops),
			}

		case recordOp:
			tx, ok := pending[rec.tx]
			if !ok {
				return fmt.Errorf("%w: operation without begin for transaction %d", ErrLogCorrupt, rec.tx)
			}
			var payload txOpPayload
			if err := json.Unmarshal(rec.payload, &payload); err != nil {
				return fmt.Errorf("%w: invalid operation payload", ErrLogCorrupt)
			}
			if payload.Index < 0 || payload.Index >= tx.ops {
				return fmt.Errorf("%w: operation index out of range", ErrLogCorrupt)
			}
			if _, exists := tx.mutations[payload.Index]; exists {
				return fmt.Errorf("%w: duplicate operation index", ErrLogCorrupt)
			}
			tx.mutations[payload.Index] = payload.Mutation

		case recordCommit:
			tx, ok := pending[rec.tx]
			if !ok {
				return fmt.Errorf("%w: commit without begin for transaction %d", ErrLogCorrupt, rec.tx)
			}
			var payload txCommitPayload
			if err := json.Unmarshal(rec.payload, &payload); err != nil {
				return fmt.Errorf("%w: invalid commit payload", ErrLogCorrupt)
			}
			if payload.Ops != tx.ops || len(tx.mutations) != tx.ops {
				return fmt.Errorf("%w: incomplete transaction %d", ErrLogCorrupt, rec.tx)
			}
			ordered := make([]mutation, tx.ops)
			for i := 0; i < tx.ops; i++ {
				m, exists := tx.mutations[i]
				if !exists {
					return fmt.Errorf("%w: missing operation %d in transaction %d", ErrLogCorrupt, i, rec.tx)
				}
				ordered[i] = m
			}
			if err := applyMutations(h, ordered); err != nil {
				return fmt.Errorf("%w: transaction %d: %v", ErrLogCorrupt, rec.tx, err)
			}
			delete(pending, rec.tx)
			h.recovery.CommittedTx++

		case recordRoll:
			if _, ok := pending[rec.tx]; ok {
				delete(pending, rec.tx)
				h.recovery.RolledBack = append(h.recovery.RolledBack, rec.tx)
			}

		default:
			return fmt.Errorf("%w: unknown record type %q", ErrLogCorrupt, rec.kind)
		}
	}

	for tx := range pending {
		h.recovery.RolledBack = append(h.recovery.RolledBack, tx)
	}
	sort.Slice(h.recovery.RolledBack, func(i, j int) bool {
		return h.recovery.RolledBack[i] < h.recovery.RolledBack[j]
	})
	h.nextTxID = maxTx + 1
	return nil
}

func (h *Host) auditReferences() {
	actual := make(map[InodeKey]uint64)
	for _, dkey := range sortedDirectoryKeys(h.dirs) {
		d := h.dirs[dkey]
		for _, entry := range d.entries {
			if _, err := CanonicalName(entry.Name); err != nil {
				h.quarantineEntries[entry.Key()] = QuarantineRecord{
					Entry:  entryKeyPtr(entry.Key()),
					Reason: QuarantineLogSemanticMismatch,
					Detail: "non-canonical directory name",
				}
				continue
			}
			key := entry.InodeKey()
			_, exists := h.inodes[key]
			if !exists {
				reason := QuarantineMissingInode
				detail := "directory entry has no matching inode"
				if h.maxGeneration[key.ID] > key.Generation {
					reason = QuarantineStaleGeneration
					detail = "directory entry references a released inode generation"
				}
				h.quarantineEntries[entry.Key()] = QuarantineRecord{
					Entry:  entryKeyPtr(entry.Key()),
					Reason: reason,
					Detail: detail,
				}
				continue
			}
			actual[key]++
			if detail, dup := h.duplicateEntries[entry.Key()]; dup {
				h.quarantineEntries[entry.Key()] = QuarantineRecord{
					Entry:  entryKeyPtr(entry.Key()),
					Reason: QuarantineDuplicateEntry,
					Detail: detail,
				}
			}
		}
	}

	activeLeases := make(map[InodeKey]uint64)
	for _, lease := range h.leases {
		if !lease.closed {
			activeLeases[lease.lease.InodeKey()]++
		}
	}

	for _, key := range sortedInodeKeys(h.inodes) {
		s := h.inodes[key]
		refs := actual[key]
		if s.linkCount != refs {
			h.quarantineInodes[key] = QuarantineRecord{
				Inode:  inodeKeyPtr(key),
				Reason: QuarantineLinkCountMismatch,
				Detail: fmt.Sprintf("linkCount=%d actualDirectoryEntries=%d", s.linkCount, refs),
			}
		}
		if s.openCount != activeLeases[key] {
			h.quarantineInodes[key] = QuarantineRecord{
				Inode:  inodeKeyPtr(key),
				Reason: QuarantineOpenCountMismatch,
				Detail: fmt.Sprintf("openCount=%d activeLeases=%d", s.openCount, activeLeases[key]),
			}
		}
		switch {
		case s.status == InodeLive && s.linkCount == 0:
			h.quarantineInodes[key] = QuarantineRecord{
				Inode:  inodeKeyPtr(key),
				Reason: QuarantineInvalidStatus,
				Detail: "Live inode has linkCount=0",
			}
		case s.linkCount > 0 && s.status != InodeLive:
			h.quarantineInodes[key] = QuarantineRecord{
				Inode:  inodeKeyPtr(key),
				Reason: QuarantineInvalidStatus,
				Detail: fmt.Sprintf("linked inode has status %s", s.status),
			}
		case s.status == InodeDeleting && s.openCount > 0:
			h.quarantineInodes[key] = QuarantineRecord{
				Inode:  inodeKeyPtr(key),
				Reason: QuarantineInvalidStatus,
				Detail: "Deleting inode still has open leases",
			}
		case s.status != InodeLive && s.status != InodeOrphan && s.status != InodeDeleting:
			h.quarantineInodes[key] = QuarantineRecord{
				Inode:  inodeKeyPtr(key),
				Reason: QuarantineInvalidStatus,
				Detail: fmt.Sprintf("unknown inode status %d", s.status),
			}
		}
	}

	for _, key := range h.quarantineMissing {
		if existing, ok := h.quarantineInodes[key]; ok {
			h.quarantineInodes[key] = existing
			continue
		}
		h.quarantineInodes[key] = QuarantineRecord{
			Inode:  inodeKeyPtr(key),
			Reason: QuarantineMissingInode,
			Detail: "log references an inode that does not exist",
		}
	}
}

func (h *Host) fillRecoveryQuarantine() {
	inodes := make([]QuarantineRecord, 0, len(h.quarantineInodes))
	for _, rec := range h.quarantineInodes {
		inodes = append(inodes, rec)
	}
	sort.Slice(inodes, func(i, j int) bool {
		a, b := inodes[i].Inode, inodes[j].Inode
		if a == nil || b == nil {
			return inodes[i].Detail < inodes[j].Detail
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.Generation < b.Generation
	})
	h.recovery.QuarantinedInodes = inodes

	entries := make([]QuarantineRecord, 0, len(h.quarantineEntries))
	for _, rec := range h.quarantineEntries {
		entries = append(entries, rec)
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i].Entry, entries[j].Entry
		if a == nil || b == nil {
			return entries[i].Detail < entries[j].Detail
		}
		if a.DirectoryID != b.DirectoryID {
			return a.DirectoryID < b.DirectoryID
		}
		if a.DirectoryGeneration != b.DirectoryGeneration {
			return a.DirectoryGeneration < b.DirectoryGeneration
		}
		return a.Name < b.Name
	})
	h.recovery.QuarantinedEntries = entries
	h.recovery.SemanticProblems = append([]string(nil), h.semanticProblems...)
}
