package canonkey

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
)

// CheckpointVersion is the on-disk checkpoint format version.
const CheckpointVersion = 1

// ErrCheckpointCorrupt is returned when a checkpoint file fails integrity
// or consistency verification during load.
var ErrCheckpointCorrupt = errors.New("canonkey: checkpoint corrupt")

type checkpointEntry struct {
	Key         string `json:"key"`
	Content     []byte `json:"content"`
	ContentHash string `json:"contentHash"`
}

type checkpointIsolated struct {
	Key      string   `json:"key"`
	Contents [][]byte `json:"contents"`
}

// checkpointPayload is the integrity-protected part of the file.
type checkpointPayload struct {
	Version  int                  `json:"version"`
	Policy   ConflictPolicy       `json:"policy"`
	Entries  []checkpointEntry    `json:"entries"`
	Isolated []checkpointIsolated `json:"isolated"`
}

type checkpointFile struct {
	Payload  checkpointPayload `json:"payload"`
	Checksum string            `json:"checksum"`
}

func payloadChecksum(p *checkpointPayload) (string, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// SaveCheckpoint atomically persists the store's full dedup state
// (canonical entries plus isolated conflicts) to path. The write goes to a
// temp file in the same directory and is renamed into place, so a crash
// mid-save never leaves a half-written checkpoint.
func (s *Store) SaveCheckpoint(path string) error {
	p := checkpointPayload{
		Version: CheckpointVersion,
		Policy:  s.policy,
	}
	for _, k := range s.Keys() {
		e := s.entries[k]
		p.Entries = append(p.Entries, checkpointEntry{
			Key:         string(k),
			Content:     e.content,
			ContentHash: contentHash(e.content),
		})
	}
	isolatedKeys := make([]string, 0, len(s.isolated))
	for k := range s.isolated {
		isolatedKeys = append(isolatedKeys, string(k))
	}
	sort.Strings(isolatedKeys)
	for _, ks := range isolatedKeys {
		p.Isolated = append(p.Isolated, checkpointIsolated{
			Key:      ks,
			Contents: s.isolated[Key(ks)],
		})
	}

	sum, err := payloadChecksum(&p)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(checkpointFile{Payload: p, Checksum: sum}, "", "  ")
	if err != nil {
		return err
	}

	tmp, err := ioutil.TempFile(filepath.Dir(path), ".canonkey-ckpt-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

// LoadCheckpoint restores a store from a checkpoint file. The file's
// integrity checksum, format version, key well-formedness, per-entry
// content hashes, and key uniqueness are all verified; any failure yields
// ErrCheckpointCorrupt (wrapped with context) and no store.
func LoadCheckpoint(path string) (*Store, error) {
	raw, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f checkpointFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("%v: %v", ErrCheckpointCorrupt, err)
	}
	sum, err := payloadChecksum(&f.Payload)
	if err != nil {
		return nil, err
	}
	if sum != f.Checksum {
		return nil, fmt.Errorf("%v: checksum mismatch", ErrCheckpointCorrupt)
	}
	if f.Payload.Version != CheckpointVersion {
		return nil, fmt.Errorf("%v: unsupported version %d", ErrCheckpointCorrupt, f.Payload.Version)
	}

	s := NewStore(f.Payload.Policy)
	for _, e := range f.Payload.Entries {
		k := Key(e.Key)
		if _, err := k.Fields(); err != nil {
			return nil, fmt.Errorf("%v: bad key encoding %q", ErrCheckpointCorrupt, e.Key)
		}
		if _, exists := s.entries[k]; exists {
			return nil, fmt.Errorf("%v: duplicate key %q", ErrCheckpointCorrupt, e.Key)
		}
		if contentHash(e.Content) != e.ContentHash {
			return nil, fmt.Errorf("%v: content hash mismatch for key %q", ErrCheckpointCorrupt, e.Key)
		}
		cp := make([]byte, len(e.Content))
		copy(cp, e.Content)
		s.entries[k] = entry{hash: sha256.Sum256(cp), content: cp}
	}
	for _, iso := range f.Payload.Isolated {
		k := Key(iso.Key)
		if _, err := k.Fields(); err != nil {
			return nil, fmt.Errorf("%v: bad isolated key encoding %q", ErrCheckpointCorrupt, iso.Key)
		}
		if _, exists := s.isolated[k]; exists {
			return nil, fmt.Errorf("%v: duplicate isolated key %q", ErrCheckpointCorrupt, iso.Key)
		}
		list := make([][]byte, len(iso.Contents))
		for i, c := range iso.Contents {
			cp := make([]byte, len(c))
			copy(cp, c)
			list[i] = cp
		}
		s.isolated[k] = list
	}
	return s, nil
}
