package netsec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

const (
	stateFileName   = "offline-auth-state.json"
	outboxFileName  = "offline-auth-outbox.json"
	tempFileSuffix  = ".tmp"
	storageFileMode = 0o600
	storageDirMode  = 0o700
)

// quotaRecord is the only per-authorization record persisted locally: the
// digest of the backend-signed grant and its remaining counter. No cleartext
// authorization fields, no signatures, no full tokens are stored.
type quotaRecord struct {
	Digest    string `json:"digest"`
	Remaining int64  `json:"remaining"`
}

// persistedState is the on-disk layout of the station-side authorization list.
type persistedState struct {
	// StationID binds the whole store to one physical station. Opening the
	// file on any other station is rejected, which neutralizes a full copy of
	// the list.
	StationID string        `json:"station_id"`
	Entries   []quotaRecord `json:"entries"`
	// NextSeq is the station-wide monotonic counter for the next receipt.
	NextSeq uint64 `json:"next_seq"`
	// ChainHead is the current hash-chain head.
	ChainHead []byte `json:"chain_head"`
}

// persistedOutbox holds receipts awaiting backend acknowledgement.
type persistedOutbox struct {
	StationID string          `json:"station_id"`
	Receipts  []SignedReceipt `json:"receipts"`
}

func statePath(dataDir string) string  { return filepath.Join(dataDir, stateFileName) }
func outboxPath(dataDir string) string { return filepath.Join(dataDir, outboxFileName) }

// atomicWriteJSON marshals value and replaces path atomically so a crash can
// never leave a half-written authorization list.
func atomicWriteJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + tempFileSuffix
	if err := os.WriteFile(tmp, data, storageFileMode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func loadJSON(path string, value any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

// snapshotState copies the in-memory state into the stable on-disk layout,
// emitting entries in digest order for deterministic files.
func (s *Station) snapshotState() persistedState {
	entries := make([]quotaRecord, 0, len(s.entries))
	for digest, remaining := range s.entries {
		entries = append(entries, quotaRecord{Digest: digest, Remaining: remaining})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Digest < entries[j].Digest })
	head := make([]byte, len(s.chainHead))
	copy(head, s.chainHead)
	return persistedState{
		StationID: s.stationID,
		Entries:   entries,
		NextSeq:   s.nextSeq,
		ChainHead: head,
	}
}

func (s *Station) persistLocked() error {
	out := persistedOutbox{StationID: s.stationID, Receipts: append([]SignedReceipt(nil), s.outbox...)}
	// Persist the outbox before advancing the state file. A crash between the
	// two writes leaves an extra receipt in the outbox rather than losing a
	// consumption; OpenStation rebuilds sequence and chain head from it.
	if err := atomicWriteJSON(outboxPath(s.dataDir), out); err != nil {
		return err
	}
	return atomicWriteJSON(statePath(s.dataDir), s.snapshotState())
}
