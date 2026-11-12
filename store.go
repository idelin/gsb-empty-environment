package taskpkg

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Entry is one persisted audit record. The storage format is intentionally
// plain JSON lines on ordinary (rollbackable) disk: durability and tamper
// detection come from the signatures and hash chain, not from the medium.
type Entry struct {
	Sequence        uint64    `json:"sequence"`
	Subject         string    `json:"subject"`
	Summary         string    `json:"summary"`
	Payload         []byte    `json:"payload,omitempty"`
	OperationDigest []byte    `json:"operation_digest"`
	PriorCommit     []byte    `json:"prior_commit"`
	Signature       []byte    `json:"signature"`
	Commitment      []byte    `json:"commitment"`
	Timestamp       time.Time `json:"timestamp"`
}

// Store is the append-only, verifiable audit log on ordinary disk.
type Store struct {
	path      string
	publicKey ed25519.PublicKey
	entries   []Entry
	last      []byte
}

// OpenStore opens (or creates) the audit log at path and fully verifies it.
// A missing file produces ErrLogMissing so that callers cannot silently
// continue after an administrator deletes the audit trail; explicit recovery
// (with a fresh counter) goes through RecoverStore.
func OpenStore(path string, publicKey ed25519.PublicKey) (*Store, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrLogMissing
		}
		return nil, err
	}
	store := &Store{path: path, publicKey: publicKey}
	if err := store.loadAndVerify(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	_ = file.Close()
	return store, nil
}

// RecoverStore creates a fresh, empty audit log at path. It must only be used
// together with a fresh monotonic counter; reusing an old counter would fail
// the open-time sequence cross-check.
func RecoverStore(path string, publicKey ed25519.PublicKey) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	store := &Store{
		path:      path,
		publicKey: publicKey,
		last:      make([]byte, sha256Size),
	}
	_ = file.Close()
	return store, nil
}

const sha256Size = 32

func (s *Store) loadAndVerify(file *os.File) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	expectedPrior := make([]byte, sha256Size)
	var expectedSeq uint64 = 1
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var entry Entry
		if err := json.Unmarshal(line, &entry); err != nil {
			return errors.Join(ErrLogTampered, err)
		}
		if err := s.verifyEntry(entry, expectedPrior, expectedSeq); err != nil {
			return err
		}
		s.entries = append(s.entries, entry)
		expectedPrior = entry.Commitment
		expectedSeq = entry.Sequence + 1
	}
	if err := scanner.Err(); err != nil {
		return errors.Join(ErrLogTampered, err)
	}
	s.last = expectedPrior
	return nil
}

func (s *Store) verifyEntry(entry Entry, expectedPrior []byte, expectedSeq uint64) error {
	if entry.Sequence != expectedSeq {
		return ErrLogTampered
	}
	if !constantTimeEqual(entry.PriorCommit, expectedPrior) {
		return ErrLogTampered
	}
	recomputedDigest := OperationDigest(Operation{
		Subject: entry.Subject,
		Summary: entry.Summary,
		Payload: entry.Payload,
	})
	if !constantTimeEqual(recomputedDigest, entry.OperationDigest) {
		return ErrLogTampered
	}
	confirmation := Confirmation{
		Sequence:        entry.Sequence,
		Subject:         entry.Subject,
		OperationDigest: entry.OperationDigest,
		PriorCommit:     entry.PriorCommit,
		Signature:       entry.Signature,
	}
	if !ed25519.Verify(s.publicKey, confirmationSigningBytes(confirmation), entry.Signature) {
		return ErrLogTampered
	}
	if !constantTimeEqual(EntryCommitment(confirmation), entry.Commitment) {
		return ErrLogTampered
	}
	return nil
}

// Append atomically adds a verified entry and fsyncs both file and directory.
// The write is a temp-file-then-rename rewrite of the whole log, so a crash
// leaves either the previous complete log or the new complete log, never a
// half-written tail.
func (s *Store) Append(entry Entry) error {
	if err := s.verifyEntry(entry, s.last, s.nextSequence()); err != nil {
		return err
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".audit-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	for _, existing := range s.entries {
		line, err := json.Marshal(existing)
		if err != nil {
			_ = tmp.Close()
			return err
		}
		if _, err := tmp.Write(append(line, '\n')); err != nil {
			_ = tmp.Close()
			return err
		}
	}
	if _, err := tmp.Write(append(encoded, '\n')); err != nil {
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
	if err := os.Rename(tmpName, s.path); err != nil {
		return err
	}
	if dirHandle, err := os.Open(dir); err == nil {
		_ = dirHandle.Sync()
		_ = dirHandle.Close()
	}

	s.entries = append(s.entries, entry)
	s.last = entry.Commitment
	return nil
}

func (s *Store) nextSequence() uint64 {
	if len(s.entries) == 0 {
		return 1
	}
	return s.entries[len(s.entries)-1].Sequence + 1
}

// LastCommitment returns the hash-chain head (all zeros for an empty log).
func (s *Store) LastCommitment() []byte {
	out := make([]byte, len(s.last))
	copy(out, s.last)
	return out
}

// HighestSequence returns the highest sequence recorded in the log (0 when
// empty).
func (s *Store) HighestSequence() uint64 {
	if len(s.entries) == 0 {
		return 0
	}
	return s.entries[len(s.entries)-1].Sequence
}

// Entries returns a defensive copy of the verified entries.
func (s *Store) Entries() []Entry {
	out := make([]Entry, len(s.entries))
	copy(out, s.entries)
	return out
}

// Close releases store resources. The current implementation holds no open
// handles between calls; the method exists for interface stability.
func (s *Store) Close() error { return nil }
