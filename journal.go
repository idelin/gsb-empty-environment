package netsec

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Journal is an append-only durability boundary for audit witnesses. Append
// must durably persist the record before returning nil. Records must return
// the complete history in sequence order.
type Journal interface {
	Append(AuditWitness) error
	Records() ([]AuditWitness, error)
}

// MemoryJournal is a strict in-memory journal used by tests and by stacks that
// provide their own durability elsewhere.
type MemoryJournal struct {
	mu      sync.Mutex
	records []AuditWitness
}

// NewMemoryJournal creates an empty strict journal.
func NewMemoryJournal() *MemoryJournal { return &MemoryJournal{} }

// NewMemoryJournalFrom creates a journal from a historical prefix. It
// validates the hash chain and refuses to accept records behind the prefix.
func NewMemoryJournalFrom(records []AuditWitness) (*MemoryJournal, error) {
	copies := copyAudit(records)
	if err := VerifyAuditChain(copies); err != nil {
		return nil, err
	}
	return &MemoryJournal{records: copies}, nil
}

// Append implements Journal and rejects stale or duplicate sequences.
func (j *MemoryJournal) Append(w AuditWitness) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := validateNextAudit(j.records, w); err != nil {
		return err
	}
	j.records = append(j.records, w)
	return nil
}

// Records implements Journal.
func (j *MemoryJournal) Records() ([]AuditWitness, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return copyAudit(j.records), nil
}

// Len returns the number of records.
func (j *MemoryJournal) Len() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.records)
}

func validateNextAudit(existing []AuditWitness, next AuditWitness) error {
	if next.Seq == 0 {
		return fmt.Errorf("%w: zero sequence", ErrJournalRollback)
	}
	if len(existing) == 0 {
		if next.Seq != 1 {
			return fmt.Errorf("%w: first record has sequence %d", ErrJournalRollback, next.Seq)
		}
		var zero [32]byte
		h := witnessHash(zero, next)
		if next.PrevHash != hex.EncodeToString(zero[:]) || next.Hash != hex.EncodeToString(h[:]) {
			return fmt.Errorf("%w: first record chain mismatch", ErrJournalCorrupt)
		}
		return nil
	}
	last := existing[len(existing)-1]
	if next.Seq != last.Seq+1 {
		return fmt.Errorf("%w: got sequence %d after %d", ErrJournalRollback, next.Seq, last.Seq)
	}
	if next.PrevHash != last.Hash {
		return fmt.Errorf("%w: previous hash mismatch", ErrJournalRollback)
	}
	prev, err := hex.DecodeString(last.Hash)
	if err != nil || len(prev) != sha256.Size {
		return fmt.Errorf("%w: previous hash encoding", ErrJournalCorrupt)
	}
	var prevHash [32]byte
	copy(prevHash[:], prev)
	h := witnessHash(prevHash, next)
	if next.Hash != hex.EncodeToString(h[:]) {
		return fmt.Errorf("%w: record hash mismatch", ErrJournalCorrupt)
	}
	return nil
}

func copyAudit(in []AuditWitness) []AuditWitness {
	out := make([]AuditWitness, len(in))
	copy(out, in)
	return out
}

func signAuditWitness(w AuditWitness, key [32]byte) string {
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write([]byte("q0183/quic/audit-record-mac/v1"))
	_, _ = mac.Write(canonicalWitnessBytes(w))
	_, _ = mac.Write([]byte(w.Hash))
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyAuditMACs authenticates every record with the recovery key. This is
// separate from VerifyAuditChain because Journal backends can validate the
// unkeyed hash chain without possessing key material.
func VerifyAuditMACs(records []AuditWitness, key [32]byte) error {
	for i, w := range records {
		mac, err := hex.DecodeString(w.MAC)
		if err != nil || len(mac) != sha256.Size {
			return fmt.Errorf("%w: record %d has invalid MAC encoding", ErrJournalCorrupt, i)
		}
		expect := signAuditWitness(w, key)
		if subtle.ConstantTimeCompare([]byte(w.MAC), []byte(expect)) != 1 {
			return fmt.Errorf("%w: record %d MAC mismatch", ErrJournalCorrupt, i)
		}
	}
	return nil
}

// VerifyAuditRecords validates both the unkeyed hash chain and the
// recovery-key MACs using this manager's key material.
func (m *Manager) VerifyAuditRecords(records []AuditWitness) error {
	key, ok := m.checkpointKey()
	if !ok {
		return ErrNoRecoveryKey
	}
	if err := VerifyAuditChain(records); err != nil {
		return err
	}
	return VerifyAuditMACs(records, key)
}

// FileJournal is an fsync'd, newline-delimited JSON audit log. A trailing
// partial line caused by a crash is ignored during recovery; corruption in the
// middle of the log is fatal.
type FileJournal struct {
	mu      sync.Mutex
	path    string
	file    *os.File
	records []AuditWitness
	closed  bool
}

// OpenFileJournal opens or creates an audit journal and validates its chain.
func OpenFileJournal(path string) (*FileJournal, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("netsec: open journal: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("netsec: read journal: %w", err)
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		// An interrupted append is not durable. Drop the partial tail so a
		// subsequent record cannot be glued onto it.
		last := bytes.LastIndexByte(data, '\n')
		if err := f.Truncate(int64(last + 1)); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("netsec: truncate partial journal tail: %w", err)
		}
		if err := f.Sync(); err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("netsec: sync truncated journal: %w", err)
		}
	}
	records, err := readJournalFile(path)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := VerifyAuditChain(records); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &FileJournal{path: path, file: f, records: records}, nil
}

func readJournalFile(path string) ([]AuditWitness, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("netsec: read journal: %w", err)
	}
	var records []AuditWitness
	complete := data
	if len(complete) > 0 && complete[len(complete)-1] != '\n' {
		complete = complete[:bytes.LastIndexByte(complete, '\n')+1]
	}
	scanner := bufio.NewScanner(bytes.NewReader(complete))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var w AuditWitness
		if err := json.Unmarshal(line, &w); err != nil {
			return nil, fmt.Errorf("%w: line %d: %v", ErrJournalCorrupt, lineNo, err)
		}
		records = append(records, w)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("netsec: scan journal: %w", err)
	}
	return records, nil
}

// Append implements Journal.
func (j *FileJournal) Append(w AuditWitness) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return errors.New("netsec: journal is closed")
	}
	if err := validateNextAudit(j.records, w); err != nil {
		return err
	}
	line, err := jsonMarshalAudit(w)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if _, err := j.file.Write(line); err != nil {
		return fmt.Errorf("netsec: append journal: %w", err)
	}
	if err := j.file.Sync(); err != nil {
		return fmt.Errorf("netsec: sync journal: %w", err)
	}
	j.records = append(j.records, w)
	return nil
}

// Records implements Journal.
func (j *FileJournal) Records() ([]AuditWitness, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return copyAudit(j.records), nil
}

// Close closes the journal file.
func (j *FileJournal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil
	}
	j.closed = true
	return j.file.Close()
}

// Checkpoint is a signed high-water mark kept outside the journal. Recovery
// refuses a journal whose sequence or digest is behind this value, which stops
// a crash-rollback from replaying already-used CID/token state.
type Checkpoint struct {
	Sequence  uint64    `json:"sequence"`
	Digest    string    `json:"digest"`
	CreatedAt time.Time `json:"created_at"`
	MAC       string    `json:"mac"`
}

func checkpointMAC(key [32]byte, sequence uint64, digest string, at time.Time) [32]byte {
	m := hmac.New(sha256.New, key[:])
	_, _ = m.Write([]byte("q0183/quic/checkpoint/v1"))
	_, _ = m.Write(appendU64(nil, sequence))
	_, _ = m.Write([]byte(digest))
	_, _ = m.Write(appendU64(nil, uint64(at.UTC().UnixNano())))
	var out [32]byte
	copy(out[:], m.Sum(nil))
	return out
}

func makeCheckpoint(key [32]byte, sequence uint64, digest string, at time.Time) Checkpoint {
	mac := checkpointMAC(key, sequence, digest, at)
	return Checkpoint{
		Sequence:  sequence,
		Digest:    digest,
		CreatedAt: at.UTC(),
		MAC:       hex.EncodeToString(mac[:]),
	}
}

// VerifyCheckpoint validates the checkpoint MAC without relying on journal contents.
func (m *Manager) VerifyCheckpoint(cp Checkpoint) error {
	key, ok := m.checkpointKey()
	if !ok {
		return ErrNoRecoveryKey
	}
	mac, err := hex.DecodeString(cp.MAC)
	if err != nil || len(mac) != sha256.Size {
		return ErrCheckpointMismatch
	}
	expect := checkpointMAC(key, cp.Sequence, cp.Digest, cp.CreatedAt)
	if subtle.ConstantTimeCompare(mac, expect[:]) != 1 {
		return ErrCheckpointMismatch
	}
	return nil
}

// Checkpoint snapshot the current hash-chain head under the recovery key.
func (m *Manager) Checkpoint() (Checkpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key, ok := m.checkpointKey()
	if !ok {
		return Checkpoint{}, ErrNoRecoveryKey
	}
	return makeCheckpoint(key, m.seq, hex.EncodeToString(m.lastHash[:]), m.clock.Now()), nil
}

// WriteCheckpointFile writes a checkpoint atomically with fsync. The file is
// kept separately from the journal so an attacker cannot roll both back
// together without invalidating the MAC. The on-disk checkpoint is monotonic:
// an attempt to replace it with an older sequence or a different digest at the
// same sequence is rejected.
func WriteCheckpointFile(path string, cp Checkpoint) error {
	if previous, err := ReadCheckpointFile(path); err == nil {
		if cp.Sequence < previous.Sequence {
			return fmt.Errorf("%w: checkpoint %d is older than %d", ErrRollbackDetected, cp.Sequence, previous.Sequence)
		}
		if cp.Sequence == previous.Sequence && cp.Digest != previous.Digest {
			return fmt.Errorf("%w: checkpoint fork at sequence %d", ErrCheckpointMismatch, cp.Sequence)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("netsec: read previous checkpoint: %w", err)
	}
	data, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	name := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, name+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write([]byte("\n")); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	if dirHandle, err := os.Open(dir); err == nil {
		_ = dirHandle.Sync()
		_ = dirHandle.Close()
	}
	return nil
}

// ReadCheckpointFile loads a checkpoint created by WriteCheckpointFile.
func ReadCheckpointFile(path string) (Checkpoint, error) {
	var cp Checkpoint
	data, err := os.ReadFile(path)
	if err != nil {
		return cp, err
	}
	if err := json.Unmarshal(bytes.TrimSpace(data), &cp); err != nil {
		return cp, fmt.Errorf("netsec: decode checkpoint: %w", err)
	}
	return cp, nil
}

// FileJournal implements io.Closer for stacks that manage resources explicitly.
var _ io.Closer = (*FileJournal)(nil)
