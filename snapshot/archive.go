package snapshot

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"compact/wire"
)

// Archive file magics.
const (
	magicCheckpoint = "CKP1"
	magicDelta      = "DLT1"
)

// ErrNoCheckpoint is returned by Load when the archive directory holds no
// checkpoint file.
var ErrNoCheckpoint = errors.New("snapshot: no checkpoint found in archive")

// Archiver persists checkpoints and incremental deltas to a directory.
// All file formats are encoded with the local wire codec.
type Archiver struct {
	dir string
	mu  sync.Mutex // serializes checkpoint/delta file writes
}

// NewArchiver creates (if needed) the archive directory.
func NewArchiver(dir string) (*Archiver, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	return &Archiver{dir: dir}, nil
}

// Dir returns the archive directory.
func (a *Archiver) Dir() string { return a.dir }

func checkpointName(version uint64) string {
	return fmt.Sprintf("checkpoint-%020d.bin", version)
}

func deltaName(from, to uint64) string {
	return fmt.Sprintf("delta-%020d-%020d.bin", from, to)
}

// Checkpoint performs a low-pause incremental checkpoint: it rotates the
// store layers under a short lock, merges and encodes the snapshot outside
// the lock, atomically writes the archive file, then installs the new base
// and compacts the journal. Returns the checkpointed version.
func (a *Archiver) Checkpoint(s *Store) (uint64, error) {
	base, overlay, version, err := s.beginCheckpoint()
	if err != nil {
		return 0, err
	}
	merged := MergeLayers(base, overlay)
	data := encodeCheckpoint(merged, version)

	a.mu.Lock()
	err = writeFileAtomic(filepath.Join(a.dir, checkpointName(version)), data)
	a.mu.Unlock()
	if err != nil {
		s.abortCheckpoint()
		return 0, err
	}
	s.finishCheckpoint(merged, version)
	return version, nil
}

// ArchiveDelta writes every mutation since `since` as an incremental delta
// file and returns the version the delta reaches. If there is nothing new,
// no file is written.
func (a *Archiver) ArchiveDelta(s *Store, since uint64) (uint64, error) {
	muts, to, err := s.DeltaSince(since)
	if err != nil {
		return 0, err
	}
	if len(muts) == 0 {
		return to, nil
	}
	data := encodeDelta(muts, since, to)
	a.mu.Lock()
	err = writeFileAtomic(filepath.Join(a.dir, deltaName(since, to)), data)
	a.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return to, nil
}

// Load restores a store from the latest checkpoint plus all subsequent
// delta files. Returns the store and the restored version.
func (a *Archiver) Load() (*Store, uint64, error) {
	ckpts, deltas, err := a.scan()
	if err != nil {
		return nil, 0, err
	}
	if len(ckpts) == 0 {
		return nil, 0, ErrNoCheckpoint
	}
	latest := ckpts[len(ckpts)-1]
	state, version, err := decodeCheckpointFile(filepath.Join(a.dir, checkpointName(latest)))
	if err != nil {
		return nil, 0, err
	}
	for _, d := range deltas {
		if d.to <= latest {
			continue
		}
		muts, _, to, err := decodeDeltaFile(filepath.Join(a.dir, deltaName(d.from, d.to)))
		if err != nil {
			return nil, 0, err
		}
		for _, m := range muts {
			if m.Version <= version {
				continue
			}
			if m.Deleted {
				delete(state, m.Key)
			} else {
				state[m.Key] = m.Value
			}
		}
		if to > version {
			version = to
		}
	}
	return NewStoreFrom(state, version), version, nil
}

type deltaRange struct{ from, to uint64 }

// scan lists checkpoint versions and delta ranges in the archive dir.
func (a *Archiver) scan() (ckpts []uint64, deltas []deltaRange, err error) {
	files, err := ioutil.ReadDir(a.dir)
	if err != nil {
		return nil, nil, err
	}
	for _, f := range files {
		name := f.Name()
		if strings.HasPrefix(name, "checkpoint-") {
			v, perr := parseVersion(name, "checkpoint-")
			if perr != nil {
				return nil, nil, perr
			}
			ckpts = append(ckpts, v)
		} else if strings.HasPrefix(name, "delta-") {
			parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(name, "delta-"), ".bin"), "-")
			if len(parts) != 2 {
				return nil, nil, fmt.Errorf("snapshot: bad delta file name %q", name)
			}
			from, err1 := strconv.ParseUint(parts[0], 10, 64)
			to, err2 := strconv.ParseUint(parts[1], 10, 64)
			if err1 != nil || err2 != nil {
				return nil, nil, fmt.Errorf("snapshot: bad delta file name %q", name)
			}
			deltas = append(deltas, deltaRange{from, to})
		}
	}
	sort.Slice(ckpts, func(i, j int) bool { return ckpts[i] < ckpts[j] })
	sort.Slice(deltas, func(i, j int) bool { return deltas[i].to < deltas[j].to })
	return ckpts, deltas, nil
}

func parseVersion(name, prefix string) (uint64, error) {
	s := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".bin")
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("snapshot: bad checkpoint file name %q", name)
	}
	return v, nil
}

// writeFileAtomic writes data to a temp file and renames it into place so
// readers never observe a partially written archive.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := ioutil.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ---- checkpoint file encoding ----
// magic(4) | varint version | repeated { tag1 bytes key, tag2 bytes value }

func encodeCheckpoint(state map[string][]byte, version uint64) []byte {
	buf := wire.NewBuffer()
	buf.AppendBytes([]byte(magicCheckpoint))
	buf.AppendVarint(version)
	keys := make([]string, 0, len(state))
	for k := range state {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		buf.AppendTag(1, wire.Bytes)
		buf.AppendBytes([]byte(k))
		buf.AppendTag(2, wire.Bytes)
		buf.AppendBytes(state[k])
	}
	return buf.Bytes()
}

func decodeCheckpointFile(path string) (map[string][]byte, uint64, error) {
	raw, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	r := wire.NewReader(raw)
	magic, err := r.ReadBytes()
	if err != nil {
		return nil, 0, fmt.Errorf("snapshot: %s: %v", path, err)
	}
	if string(magic) != magicCheckpoint {
		return nil, 0, fmt.Errorf("snapshot: %s: bad magic", path)
	}
	version, err := r.ReadVarint()
	if err != nil {
		return nil, 0, fmt.Errorf("snapshot: %s: %v", path, err)
	}
	state := make(map[string][]byte)
	for !r.EOF() {
		num, wt, err := r.ReadTag()
		if err != nil {
			return nil, 0, fmt.Errorf("snapshot: %s: %v", path, err)
		}
		if num != 1 || wt != wire.Bytes {
			return nil, 0, fmt.Errorf("snapshot: %s: unexpected field %d", path, num)
		}
		key, err := r.ReadBytes()
		if err != nil {
			return nil, 0, fmt.Errorf("snapshot: %s: %v", path, err)
		}
		num, wt, err = r.ReadTag()
		if err != nil {
			return nil, 0, fmt.Errorf("snapshot: %s: %v", path, err)
		}
		if num != 2 || wt != wire.Bytes {
			return nil, 0, fmt.Errorf("snapshot: %s: unexpected field %d", path, num)
		}
		val, err := r.ReadBytes()
		if err != nil {
			return nil, 0, fmt.Errorf("snapshot: %s: %v", path, err)
		}
		state[string(key)] = append([]byte(nil), val...)
	}
	return state, version, nil
}

// ---- delta file encoding ----
// magic(4) | varint from | varint to |
// repeated { tag1 bytes key, [tag2 bytes value], tag3 varint version }

func encodeDelta(muts []Mutation, from, to uint64) []byte {
	buf := wire.NewBuffer()
	buf.AppendBytes([]byte(magicDelta))
	buf.AppendVarint(from)
	buf.AppendVarint(to)
	for _, m := range muts {
		buf.AppendTag(1, wire.Bytes)
		buf.AppendBytes([]byte(m.Key))
		if !m.Deleted {
			buf.AppendTag(2, wire.Bytes)
			buf.AppendBytes(m.Value)
		}
		buf.AppendTag(3, wire.Varint)
		buf.AppendVarint(m.Version)
	}
	return buf.Bytes()
}

func decodeDeltaFile(path string) (muts []Mutation, from, to uint64, err error) {
	raw, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, 0, 0, err
	}
	r := wire.NewReader(raw)
	magic, err := r.ReadBytes()
	if err != nil {
		return nil, 0, 0, fmt.Errorf("snapshot: %s: %v", path, err)
	}
	if string(magic) != magicDelta {
		return nil, 0, 0, fmt.Errorf("snapshot: %s: bad magic", path)
	}
	if from, err = r.ReadVarint(); err != nil {
		return nil, 0, 0, fmt.Errorf("snapshot: %s: %v", path, err)
	}
	if to, err = r.ReadVarint(); err != nil {
		return nil, 0, 0, fmt.Errorf("snapshot: %s: %v", path, err)
	}
	for !r.EOF() {
		num, wt, err := r.ReadTag()
		if err != nil {
			return nil, 0, 0, fmt.Errorf("snapshot: %s: %v", path, err)
		}
		if num != 1 || wt != wire.Bytes {
			return nil, 0, 0, fmt.Errorf("snapshot: %s: unexpected field %d", path, num)
		}
		key, err := r.ReadBytes()
		if err != nil {
			return nil, 0, 0, fmt.Errorf("snapshot: %s: %v", path, err)
		}
		m := Mutation{Key: string(key), Deleted: true}
		// optional value field, then mandatory version field
		num, wt, err = r.ReadTag()
		if err != nil {
			return nil, 0, 0, fmt.Errorf("snapshot: %s: %v", path, err)
		}
		if num == 2 {
			if wt != wire.Bytes {
				return nil, 0, 0, fmt.Errorf("snapshot: %s: bad value wire type", path)
			}
			val, err := r.ReadBytes()
			if err != nil {
				return nil, 0, 0, fmt.Errorf("snapshot: %s: %v", path, err)
			}
			m.Value = append([]byte(nil), val...)
			m.Deleted = false
			num, wt, err = r.ReadTag()
			if err != nil {
				return nil, 0, 0, fmt.Errorf("snapshot: %s: %v", path, err)
			}
		}
		if num != 3 || wt != wire.Varint {
			return nil, 0, 0, fmt.Errorf("snapshot: %s: unexpected field %d", path, num)
		}
		if m.Version, err = r.ReadVarint(); err != nil {
			return nil, 0, 0, fmt.Errorf("snapshot: %s: %v", path, err)
		}
		muts = append(muts, m)
	}
	return muts, from, to, nil
}
