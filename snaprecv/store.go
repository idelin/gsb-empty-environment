package snaprecv

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Layout under the root directory:
//
//	current.json          pointer to the installed version (atomic rename)
//	snapshots/v<N>        immutable installed snapshot files
//	staging/v<N>/manifest.json   staged meta + per-chunk validated flags
//	pool/<sha256hex>      validated chunks, content-addressed, shared
//
// Staging and the pool are the only places written before full
// validation; the visible state (current.json + snapshots/) changes only
// via atomic rename during Install.
type Store struct {
	mu   sync.Mutex
	dir  string
	hook Hook

	installed uint64
	hasInst   bool
	staging   map[uint64]*stagingState
}

type stagingState struct {
	meta      Meta
	validated []bool
}

type manifestFile struct {
	Meta      Meta   `json:"meta"`
	Validated []bool `json:"validated"`
}

type pointerFile struct {
	Version uint64 `json:"version"`
}

// Open creates or recovers a Store rooted at dir. Recovery reloads the
// installed pointer and every staged manifest: chunks already validated
// before a crash are reused without re-delivery or re-hashing, staging
// for superseded versions is discarded, and half-installed snapshots are
// rolled back to the last fully installed version.
func Open(dir string, hook Hook) (*Store, error) {
	s := &Store{dir: dir, hook: hook, staging: map[uint64]*stagingState{}}
	for _, sub := range []string{"snapshots", "staging", "pool"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}

	// Recover the installed pointer. A torn pointer write is impossible
	// (rename is atomic); a missing pointer means nothing was installed.
	ptrBytes, err := os.ReadFile(filepath.Join(dir, "current.json"))
	switch {
	case err == nil:
		var ptr pointerFile
		if err := json.Unmarshal(ptrBytes, &ptr); err != nil {
			return nil, fmt.Errorf("snaprecv: corrupt pointer: %w", err)
		}
		if _, err := os.Stat(snapshotPath(dir, ptr.Version)); err != nil {
			// Pointer renamed but snapshot rename lost: cannot happen on
			// POSIX (snapshot rename precedes pointer rename), but treat
			// as nothing installed rather than serving a missing file.
			return nil, fmt.Errorf("snaprecv: pointer targets missing snapshot: %w", err)
		}
		s.installed, s.hasInst = ptr.Version, true
	case os.IsNotExist(err):
	default:
		return nil, err
	}

	// Drop orphaned temp assembly files from interrupted installs.
	tmpFiles, _ := filepath.Glob(filepath.Join(dir, "snapshots", "tmp-*"))
	for _, f := range tmpFiles {
		os.Remove(f)
	}

	// Reload staged manifests; discard anything not newer than installed.
	entries, err := os.ReadDir(filepath.Join(dir, "staging"))
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		ver, ok := parseVersionDir(e.Name())
		if !ok {
			continue
		}
		if s.hasInst && ver <= s.installed {
			os.RemoveAll(filepath.Join(dir, "staging", e.Name()))
			continue
		}
		var mf manifestFile
		b, err := os.ReadFile(filepath.Join(dir, "staging", e.Name(), "manifest.json"))
		if err != nil || json.Unmarshal(b, &mf) != nil || mf.Meta.valid() != nil || len(mf.Validated) != mf.Meta.NumChunks() {
			// Torn or corrupt manifest: restart staging for this version.
			os.RemoveAll(filepath.Join(dir, "staging", e.Name()))
			continue
		}
		// Re-check that each validated chunk still exists in the pool;
		// a crash between manifest rename and pool rename is impossible
		// (pool rename precedes manifest rename), but stay conservative.
		for i, ok := range mf.Validated {
			if ok {
				if _, err := os.Stat(poolPath(dir, mf.Meta.Chunks[i])); err != nil {
					mf.Validated[i] = false
				}
			}
		}
		s.staging[ver] = &stagingState{meta: mf.Meta, validated: mf.Validated}
	}
	return s, nil
}

// InstalledVersion reports the installed version and whether one exists.
func (s *Store) InstalledVersion() (uint64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.installed, s.hasInst
}

// Offer registers the manifest for a version and returns the indices of
// chunks still needed. Manifests for versions not newer than the
// installed snapshot are rejected so stale responses can never displace
// newer state. Re-offering an identical manifest is a no-op that
// re-reports missing chunks (used after recovery).
func (s *Store) Offer(meta Meta) ([]int, error) {
	if err := meta.valid(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasInst && meta.Version <= s.installed {
		return nil, ErrStaleVersion
	}
	if st, ok := s.staging[meta.Version]; ok {
		if !st.meta.equal(meta) {
			return nil, ErrMetaConflict
		}
		return missingChunks(st), nil
	}
	st := &stagingState{meta: meta, validated: make([]bool, meta.NumChunks())}
	if err := s.persistManifestLocked(meta.Version, &manifestFile{Meta: meta, Validated: st.validated}); err != nil {
		return nil, err
	}
	s.staging[meta.Version] = st
	return missingChunks(st), nil
}

func missingChunks(st *stagingState) []int {
	var out []int
	for i, ok := range st.validated {
		if !ok {
			out = append(out, i)
		}
	}
	return out
}

// WriteChunk validates one chunk against the manifest hash and expected
// length (the tail chunk may be short), then durably records it in the
// staging pool. Chunks may arrive in any order and may be duplicates;
// a chunk whose bytes do not match the manifest hash is rejected without
// disturbing any previously validated chunk at the same offset.
func (s *Store) WriteChunk(version uint64, index int, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasInst && version <= s.installed {
		return ErrStaleVersion
	}
	st, ok := s.staging[version]
	if !ok {
		return ErrUnknownVersion
	}
	if index < 0 || index >= st.meta.NumChunks() {
		return fmt.Errorf("snaprecv: chunk index %d out of range", index)
	}
	if int64(len(data)) != st.meta.ChunkLen(index) {
		return ErrChunkLength
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != st.meta.Chunks[index] {
		return ErrChunkHashMismatch
	}
	if st.validated[index] {
		return nil // duplicate delivery of an identical chunk
	}
	if err := s.persistChunkLocked(st.meta.Chunks[index], data); err != nil {
		return err
	}
	st.validated[index] = true
	return s.persistManifestLocked(version, &manifestFile{Meta: st.meta, Validated: st.validated})
}

// Validate fully re-verifies a staged snapshot: every chunk present and
// the overall SHA-256 matching the manifest. It reads only the staging
// pool and never touches visible state.
func (s *Store) Validate(version uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.staging[version]
	if !ok {
		return ErrUnknownVersion
	}
	for _, ok := range st.validated {
		if !ok {
			return ErrIncomplete
		}
	}
	h := sha256.New()
	for i := range st.meta.Chunks {
		f, err := os.Open(poolPath(s.dir, st.meta.Chunks[i]))
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(h, f)
		f.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	if hex.EncodeToString(h.Sum(nil)) != st.meta.Hash {
		return fmt.Errorf("snaprecv: overall hash mismatch")
	}
	return nil
}

// Install atomically replaces the visible state with a fully validated
// snapshot. Older versions can never be installed over newer ones. A
// crash at any point leaves either the previous or the new snapshot
// visible, never a torn one.
func (s *Store) Install(version uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasInst && version <= s.installed {
		return ErrStaleVersion
	}
	st, ok := s.staging[version]
	if !ok {
		return ErrUnknownVersion
	}
	for _, ok := range st.validated {
		if !ok {
			return ErrIncomplete
		}
	}

	// 1. Assemble into a temp file in snapshots/ and fsync.
	tmp := filepath.Join(s.dir, "snapshots", fmt.Sprintf("tmp-%d", version))
	if err := assemble(tmp, s.dir, st.meta); err != nil {
		return err
	}
	if err := s.trip(CutSnapshotBeforeRename); err != nil {
		return err
	}
	// 2. Rename to the immutable versioned name.
	if err := os.Rename(tmp, snapshotPath(s.dir, version)); err != nil {
		return err
	}
	if err := fsyncDir(filepath.Join(s.dir, "snapshots")); err != nil {
		return err
	}

	// 3. Atomically swap the visible pointer.
	ptrBytes, _ := json.Marshal(pointerFile{Version: version})
	ptrTmp := filepath.Join(s.dir, "current.json.tmp")
	if err := writeFileSync(ptrTmp, ptrBytes); err != nil {
		return err
	}
	if err := s.trip(CutPointerBeforeRename); err != nil {
		return err
	}
	if err := os.Rename(ptrTmp, filepath.Join(s.dir, "current.json")); err != nil {
		return err
	}
	if err := s.trip(CutPointerAfterRename); err != nil {
		return err
	}
	if err := fsyncDir(s.dir); err != nil {
		return err
	}

	s.installed, s.hasInst = version, true
	// Staging for the installed and any older versions is now useless.
	for v, dirName := range s.stagingDirsLocked() {
		if v <= version {
			os.RemoveAll(dirName)
			delete(s.staging, v)
		}
	}
	return nil
}

// ReadInstalled opens the currently installed snapshot.
func (s *Store) ReadInstalled() (io.ReadCloser, Meta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasInst {
		return nil, Meta{}, ErrNotInstalled
	}
	f, err := os.Open(snapshotPath(s.dir, s.installed))
	if err != nil {
		return nil, Meta{}, err
	}
	return f, Meta{Version: s.installed}, nil
}

// Staged reports the staged meta and validated-chunk bitmap for a
// version, for observability and tests.
func (s *Store) Staged(version uint64) (Meta, []bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.staging[version]
	if !ok {
		return Meta{}, nil, false
	}
	flags := append([]bool(nil), st.validated...)
	return st.meta, flags, true
}

func (s *Store) trip(cp CutPoint) error {
	if s.hook != nil {
		return s.hook(cp)
	}
	return nil
}

func (s *Store) stagingDirsLocked() map[uint64]string {
	out := map[uint64]string{}
	for v := range s.staging {
		out[v] = filepath.Join(s.dir, "staging", versionDir(v))
	}
	return out
}

// persistChunkLocked writes data into the content-addressed pool via
// temp-file + fsync + atomic rename. Existing entries are reused, which
// is what lets recovery skip re-validated chunks.
func (s *Store) persistChunkLocked(hashHex string, data []byte) error {
	dst := poolPath(s.dir, hashHex)
	if _, err := os.Stat(dst); err == nil {
		return nil
	}
	tmp := dst + ".tmp"
	if err := writeFileSync(tmp, data); err != nil {
		return err
	}
	if err := s.trip(CutChunkBeforeRename); err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return err
	}
	return fsyncDir(filepath.Join(s.dir, "pool"))
}

func (s *Store) persistManifestLocked(version uint64, mf *manifestFile) error {
	dir := filepath.Join(s.dir, "staging", versionDir(version))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(mf)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "manifest.json.tmp")
	if err := writeFileSync(tmp, b); err != nil {
		return err
	}
	if err := s.trip(CutManifestBeforeRename); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(dir, "manifest.json")); err != nil {
		return err
	}
	return fsyncDir(dir)
}

func assemble(tmp, root string, meta Meta) error {
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	for i := range meta.Chunks {
		c, err := os.Open(poolPath(root, meta.Chunks[i]))
		if err != nil {
			f.Close()
			return err
		}
		_, copyErr := io.Copy(f, c)
		c.Close()
		if copyErr != nil {
			f.Close()
			return copyErr
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func writeFileSync(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func snapshotPath(root string, version uint64) string {
	return filepath.Join(root, "snapshots", versionDir(version))
}

func poolPath(root, hashHex string) string {
	return filepath.Join(root, "pool", hashHex)
}

func versionDir(v uint64) string { return "v" + strconv.FormatUint(v, 10) }

func parseVersionDir(name string) (uint64, bool) {
	if !strings.HasPrefix(name, "v") {
		return 0, false
	}
	v, err := strconv.ParseUint(name[1:], 10, 64)
	return v, err == nil
}

// GC removes pool chunks not referenced by any staged manifest or the
// installed snapshot. Optional housekeeping; safe to call any time.
func (s *Store) GC() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	used := map[string]bool{}
	for _, st := range s.staging {
		for i, ok := range st.validated {
			if ok {
				used[st.meta.Chunks[i]] = true
			}
		}
	}
	entries, err := os.ReadDir(filepath.Join(s.dir, "pool"))
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".tmp") || !used[name] {
			os.Remove(filepath.Join(s.dir, "pool", name))
		}
	}
	return nil
}
