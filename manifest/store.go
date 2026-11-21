package manifest

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	stateFileMagic = "BMS1"
	stateFileName  = "manifest-state.bin"
	tmpSuffix      = ".tmp"
)

// MemoryStore is a Store for tests and non-persistent hosts. FailToSave, when
// non-nil, is consulted before every commit and makes Save fail without
// mutating stored state, simulating a failed final persistence write.
type MemoryStore struct {
	state       *State
	failToSave  func(*State) error
	saveAttempt int
}

// NewMemoryStore creates an empty in-memory store.
func NewMemoryStore() *MemoryStore { return &MemoryStore{} }

// SetSaveFault installs a hook consulted before each Save.
func (s *MemoryStore) SetSaveFault(fn func(*State) error) {
	s.failToSave = fn
}

// SaveAttempts reports how many Save calls (including failed ones) occurred.
func (s *MemoryStore) SaveAttempts() int { return s.saveAttempt }

// Load returns the last successfully committed state.
func (s *MemoryStore) Load() (*State, error) {
	if s.state == nil {
		return nil, ErrNoState
	}
	cp := *s.state
	return &cp, nil
}

// Save atomically swaps the committed state; on failure the old state survives.
func (s *MemoryStore) Save(state *State) error {
	s.saveAttempt++
	if s.failToSave != nil {
		if err := s.failToSave(state); err != nil {
			return err
		}
	}
	encoded, err := encodeState(state)
	if err != nil {
		return err
	}
	decoded, err := decodeState(encoded)
	if err != nil {
		return err
	}
	s.state = decoded
	return nil
}

// FileStore persists state to a single file. Writes go to a sibling temp file
// which is fsynced and then atomically renamed over the live file, so a crash
// can never leave a new generation paired with an old manifest body.
type FileStore struct {
	dir string
	// BeforeRename, if set, runs after the temp file is fsynced but before the
	// final rename, allowing tests to simulate a crash at the last instant.
	BeforeRename func(tmpPath string) error
}

// NewFileStore creates a file-backed store inside dir, creating dir if needed.
func NewFileStore(dir string) (*FileStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("%w: empty store directory", ErrCorruptState)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &FileStore{dir: dir}, nil
}

func (s *FileStore) path() string    { return filepath.Join(s.dir, stateFileName) }
func (s *FileStore) tmpPath() string { return filepath.Join(s.dir, stateFileName+tmpSuffix) }

// Load reads and integrity-checks the persisted state.
func (s *FileStore) Load() (*State, error) {
	raw, err := os.ReadFile(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoState
	}
	if err != nil {
		return nil, err
	}
	return decodeState(raw)
}

// Save commits state via write-temp + fsync + atomic rename.
func (s *FileStore) Save(state *State) error {
	encoded, err := encodeState(state)
	if err != nil {
		return err
	}
	tmp := s.tmpPath()
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(encoded); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if s.BeforeRename != nil {
		if err := s.BeforeRename(tmp); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, s.path())
}

// State file layout:

//	"BMS1" | bodyLen(u64) | body | sha256(body)
//
// The body itself is length-prefixed canonical manifest bytes plus generation
// and acceptance timestamp; loading re-derives the manifest digest and rejects
// any record whose digest does not match the stored one.
func encodeState(state *State) ([]byte, error) {
	if state == nil {
		return nil, fmt.Errorf("%w: nil state", ErrCorruptState)
	}
	manifestBytes, err := EncodeManifest(state.Manifest)
	if err != nil {
		return nil, err
	}
	digest := sha256Sum(manifestBytes)
	if !bytes.Equal(digest, state.Digest) {
		return nil, fmt.Errorf("%w: state digest does not match manifest", ErrCorruptState)
	}
	if state.Generation != state.Manifest.Generation {
		return nil, fmt.Errorf("%w: state generation disagrees with manifest", ErrCorruptState)
	}

	var body []byte
	putUint64(&body, tagUint64, state.Generation)
	putUint64(&body, tagTime, uint64(state.AcceptedAt.UTC().UnixMilli()))
	putBytes(&body, tagBytes, digest)
	putBytes(&body, tagBytes, manifestBytes)

	out := make([]byte, 0, len(stateFileMagic)+8+len(body)+sha256.Size)
	out = append(out, stateFileMagic...)
	putU64(&out, uint64(len(body)))
	out = append(out, body...)
	sum := sha256.Sum256(body)
	out = append(out, sum[:]...)
	return out, nil
}

func decodeState(raw []byte) (*State, error) {
	if len(raw) < len(stateFileMagic)+8+sha256.Size || string(raw[:4]) != stateFileMagic {
		return nil, fmt.Errorf("%w: bad magic or truncated", ErrCorruptState)
	}
	bodyLen := int(binary.BigEndian.Uint64(raw[4:12]))
	bodyEnd := 12 + bodyLen
	if bodyLen <= 0 || bodyEnd+sha256.Size != len(raw) {
		return nil, fmt.Errorf("%w: length mismatch", ErrCorruptState)
	}
	body := raw[12:bodyEnd]
	storedSum := raw[bodyEnd:]
	calcSum := sha256.Sum256(body)
	if !bytes.Equal(calcSum[:], storedSum) {
		return nil, fmt.Errorf("%w: checksum mismatch", ErrCorruptState)
	}

	d := &decoder{data: body}
	generation, err := d.readUint64(tagUint64)
	if err != nil {
		return nil, err
	}
	acceptedMillis, err := d.readUint64(tagTime)
	if err != nil {
		return nil, err
	}
	digest, err := d.readTagLen(tagBytes)
	if err != nil {
		return nil, err
	}
	manifestBytes, err := d.readTagLen(tagBytes)
	if err != nil {
		return nil, err
	}
	if d.pos != len(body) {
		return nil, fmt.Errorf("%w: trailing state bytes", ErrCorruptState)
	}

	m, err := decodeCanonicalManifest(manifestBytes)
	if err != nil {
		return nil, err
	}
	if m.Generation != generation {
		return nil, fmt.Errorf("%w: generation paired with mismatched manifest body", ErrCorruptState)
	}
	wantDigest := sha256Sum(manifestBytes)
	if !bytes.Equal(wantDigest, digest) {
		return nil, fmt.Errorf("%w: digest paired with mismatched manifest body", ErrCorruptState)
	}

	return &State{
		Generation: generation,
		Digest:     append([]byte(nil), digest...),
		AcceptedAt: time.UnixMilli(int64(acceptedMillis)).UTC(),
		Manifest:   m,
	}, nil
}
