package corpus

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Entry describes one stored sample: its content digest, length in bytes and
// the set of coverage edge IDs attributed to it.
type Entry struct {
	Digest string   `json:"digest"`
	Length int      `json:"length"`
	Edges  []uint64 `json:"edges"`
}

// index is the immutable snapshot published after every mutation.
type index struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

const indexVersion = 1

type metaFile struct {
	Edges []uint64 `json:"edges"`
}

func (s *Store) samplesDir() string { return filepath.Join(s.dir, "samples") }
func (s *Store) indexDir() string   { return filepath.Join(s.dir, "index") }

func (s *Store) samplePath(digest string) string {
	return filepath.Join(s.samplesDir(), digest)
}

func (s *Store) metaPath(digest string) string {
	return filepath.Join(s.samplesDir(), digest+".meta")
}

func (s *Store) indexPath(seq int) string {
	return filepath.Join(s.indexDir(), strconv.Itoa(seq)+".json")
}

// putFile writes data to path and fsyncs it, running the fault hook before
// the write and before the sync.
func (s *Store) putFile(path string, data []byte, writeOp, syncOp string) error {
	if err := s.check(writeOp); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := s.check(syncOp); err != nil {
		return err
	}
	return f.Sync()
}

func (s *Store) syncDir(path string) error {
	if err := s.check("sync-dir"); err != nil {
		return err
	}
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// publish writes idx as a new immutable index file and atomically points
// "current" at it via rename. Sample files referenced by idx must already be
// durable on disk before publish is called.
func (s *Store) publish(idx *index) error {
	s.seq++
	seq := s.seq
	data, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	tmp := s.indexPath(seq) + ".tmp"
	if err := s.putFile(tmp, data, "write-index", "sync-index"); err != nil {
		return err
	}
	if err := s.check("rename-index"); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.indexPath(seq)); err != nil {
		return err
	}
	ctmp := filepath.Join(s.dir, "current.tmp")
	if err := s.putFile(ctmp, []byte(strconv.Itoa(seq)), "write-current", "sync-current"); err != nil {
		return err
	}
	if err := s.check("rename-current"); err != nil {
		return err
	}
	if err := os.Rename(ctmp, filepath.Join(s.dir, "current")); err != nil {
		return err
	}
	return s.syncDir(s.dir)
}

// loadIndex reads the currently published index without mutating anything.
func (s *Store) loadIndex() (*index, error) {
	cur, err := os.ReadFile(filepath.Join(s.dir, "current"))
	if err != nil {
		return nil, err
	}
	seq, err := strconv.Atoi(strings.TrimSpace(string(cur)))
	if err != nil {
		return nil, fmt.Errorf("corpus: bad current pointer: %w", err)
	}
	data, err := os.ReadFile(s.indexPath(seq))
	if err != nil {
		return nil, err
	}
	var idx index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("corpus: bad index %d: %w", seq, err)
	}
	if idx.Version != indexVersion {
		return nil, fmt.Errorf("corpus: unsupported index version %d", idx.Version)
	}
	s.seq = seq
	return &idx, nil
}

// filesExist reports whether every entry references an existing sample file.
func (s *Store) filesExist(idx *index) bool {
	for _, e := range idx.Entries {
		if _, err := os.Stat(s.samplePath(e.Digest)); err != nil {
			return false
		}
	}
	return true
}

// lockedIndex returns a usable index, rebuilding it from the sample files on
// disk when the published index is missing, corrupt, or references files that
// no longer exist. The rebuilt index is published before returning.
func (s *Store) lockedIndex() (*index, error) {
	if idx, err := s.loadIndex(); err == nil && s.filesExist(idx) {
		return idx, nil
	}
	return s.rebuild()
}

// rebuild scans the samples directory and reconstructs the index from the
// sample/metadata pairs actually present on disk.
func (s *Store) rebuild() (*index, error) {
	entries, err := os.ReadDir(s.samplesDir())
	if err != nil {
		return nil, err
	}
	idx := &index{Version: indexVersion}
	for _, de := range entries {
		name := de.Name()
		if de.IsDir() || strings.HasSuffix(name, ".meta") {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		meta, err := os.ReadFile(s.metaPath(name))
		if err != nil {
			// Sample without durable metadata: its Add never completed,
			// so it was never acknowledged. Leave it unreferenced.
			continue
		}
		var mf metaFile
		if err := json.Unmarshal(meta, &mf); err != nil {
			continue
		}
		idx.Entries = append(idx.Entries, Entry{
			Digest: name,
			Length: int(info.Size()),
			Edges:  normEdges(mf.Edges),
		})
	}
	sortEntries(idx.Entries)
	if err := s.publish(idx); err != nil {
		return nil, err
	}
	return idx, nil
}

// pruneIndexes removes index files older than the current sequence number.
// Best-effort: stale index files are harmless.
func (s *Store) pruneIndexes() {
	entries, err := os.ReadDir(s.indexDir())
	if err != nil {
		return
	}
	keep := strconv.Itoa(s.seq) + ".json"
	for _, de := range entries {
		if de.Name() != keep {
			os.Remove(filepath.Join(s.indexDir(), de.Name()))
		}
	}
}

// nextSeq recovers the sequence counter from existing index files.
func (s *Store) nextSeq() {
	entries, err := os.ReadDir(s.indexDir())
	if err != nil {
		return
	}
	for _, de := range entries {
		n, err := strconv.Atoi(strings.TrimSuffix(de.Name(), ".json"))
		if err == nil && n > s.seq {
			s.seq = n
		}
	}
}

func normEdges(edges []uint64) []uint64 {
	out := make([]uint64, len(edges))
	copy(out, edges)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	n := 0
	for _, e := range out {
		if n == 0 || out[n-1] != e {
			out[n] = e
			n++
		}
	}
	return out[:n]
}

func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Length != entries[j].Length {
			return entries[i].Length < entries[j].Length
		}
		return entries[i].Digest < entries[j].Digest
	})
}

func unionEdges(a, b []uint64) []uint64 {
	set := make(map[uint64]struct{}, len(a)+len(b))
	for _, e := range a {
		set[e] = struct{}{}
	}
	for _, e := range b {
		set[e] = struct{}{}
	}
	out := make([]uint64, 0, len(set))
	for e := range set {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func equalEdges(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

var errNotFound = errors.New("corpus: entry not found")

func (idx *index) find(digest string) (*Entry, error) {
	for i := range idx.Entries {
		if idx.Entries[i].Digest == digest {
			return &idx.Entries[i], nil
		}
	}
	return nil, errNotFound
}
