package aries

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// snapshotMagic identifies snapshot files and versions their format.
const snapshotMagic = "ARSNAP01"

// SnapshotMeta describes one snapshot generation.
type SnapshotMeta struct {
	Generation int       `json:"generation"`
	Full       bool      `json:"full"` // base snapshot vs incremental delta
	LSN        uint64    `json:"lsn"`  // WAL LSN at capture time
	Pages      int       `json:"pages"`
	File       string    `json:"file"`
	CreatedAt  time.Time `json:"created_at"`
}

// manifest is the on-disk JSON index of the snapshot chain.
type manifest struct {
	Snapshots []SnapshotMeta `json:"snapshots"`
}

// Snapshotter produces lightweight incremental snapshots of the buffer
// pool. The first snapshot is a full base image; subsequent snapshots
// contain only pages changed since the previous one. The pool is locked
// only for the in-memory copy of changed pages, so the pause is
// proportional to the delta, not the database size. Safe for concurrent
// use with ongoing transactions.
type Snapshotter struct {
	mu     sync.Mutex
	dir    string
	pool   *BufferPool
	wal    *WAL
	gen    int
	closed bool
}

// NewSnapshotter creates a snapshotter storing files under dir. If dir
// already contains snapshots, the generation counter resumes after them.
func NewSnapshotter(dir string, pool *BufferPool, wal *WAL) (*Snapshotter, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	s := &Snapshotter{dir: dir, pool: pool, wal: wal}
	m, err := readManifest(dir)
	if err == nil && len(m.Snapshots) > 0 {
		s.gen = m.Snapshots[len(m.Snapshots)-1].Generation + 1
	}
	return s, nil
}

// Take captures a snapshot and returns its metadata. Generation 0 (or the
// first after losing the chain) is a full snapshot; later ones are
// incremental deltas. An incremental snapshot may legitimately contain
// zero pages when nothing changed.
func (s *Snapshotter) Take() (SnapshotMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return SnapshotMeta{}, fmt.Errorf("snapshotter: closed")
	}
	if err := s.wal.Flush(); err != nil {
		return SnapshotMeta{}, err
	}
	lsn := s.wal.LastLSN()

	full := s.gen == 0
	var pages map[uint32]SnapshotPage
	var err error
	if full {
		pages, err = s.pool.CollectAll()
	} else {
		pages = s.pool.CollectDelta()
	}
	if err != nil {
		return SnapshotMeta{}, err
	}

	meta := SnapshotMeta{
		Generation: s.gen,
		Full:       full,
		LSN:        lsn,
		Pages:      len(pages),
		File:       fmt.Sprintf("snap-%06d.snap", s.gen),
		CreatedAt:  time.Now().UTC(),
	}
	// Disk write happens outside the pool lock: only the snapshotter's own
	// mutex is held, so transactions proceed during the I/O.
	if err := writeSnapshotFile(filepath.Join(s.dir, meta.File), meta, pages); err != nil {
		return SnapshotMeta{}, err
	}

	m, err := readManifest(s.dir)
	if err != nil {
		m = &manifest{}
	}
	m.Snapshots = append(m.Snapshots, meta)
	if err := writeManifest(s.dir, m); err != nil {
		return SnapshotMeta{}, err
	}
	s.gen++
	return meta, nil
}

// Chain returns the snapshot metadata in generation order.
func (s *Snapshotter) Chain() ([]SnapshotMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, err := readManifest(s.dir)
	if err != nil {
		return nil, err
	}
	out := make([]SnapshotMeta, len(m.Snapshots))
	copy(out, m.Snapshots)
	return out, nil
}

// Close marks the snapshotter closed. It is idempotent and launches no
// background goroutines, so there is nothing to join.
func (s *Snapshotter) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// RestoreSnapshots replays the snapshot chain stored in dir onto store:
// the base image first, then each delta in generation order. Pages never
// touched by any snapshot keep their current store contents.
func RestoreSnapshots(dir string, store *PageStore) error {
	m, err := readManifest(dir)
	if err != nil {
		return err
	}
	if len(m.Snapshots) == 0 {
		return fmt.Errorf("snapshot: empty manifest in %s", dir)
	}
	snaps := make([]SnapshotMeta, len(m.Snapshots))
	copy(snaps, m.Snapshots)
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].Generation < snaps[j].Generation })
	if !snaps[0].Full {
		return fmt.Errorf("snapshot: chain in %s does not start with a full snapshot", dir)
	}
	for _, meta := range snaps {
		pages, err := readSnapshotFile(filepath.Join(dir, meta.File))
		if err != nil {
			return err
		}
		for id, sp := range pages {
			if err := store.Write(id, sp.PageLSN, sp.Data); err != nil {
				return err
			}
		}
	}
	return store.Sync()
}

// ArchiveCheckpoint archives (and prunes from the live log) every WAL
// record strictly older than the END_CHKPT record at chkLSN, keeping
// records of still-active transactions. Callers should flush dirty pages
// (BufferPool.FlushAll) before archiving so the pruned history is fully
// materialized in the page file / snapshots.
func ArchiveCheckpoint(wal *WAL, pool *BufferPool, archiveDir string, chkLSN uint64, keepTxns map[uint64]bool) (string, int, error) {
	if chkLSN == 0 {
		return "", 0, fmt.Errorf("archive: checkpoint LSN must be > 0")
	}
	if err := pool.FlushAll(); err != nil {
		return "", 0, err
	}
	// Keep the END_CHKPT record itself in the live log so analysis can
	// still bootstrap from it.
	return wal.Archive(archiveDir, chkLSN-1, keepTxns)
}

// --- snapshot file format -------------------------------------------------
// magic(8) gen(8) lsn(8) count(4) [ pageID(4) pageLSN(8) dataLen(4) data ]*

func writeSnapshotFile(path string, meta SnapshotMeta, pages map[uint32]SnapshotPage) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 256*1024)
	hdr := make([]byte, 0, 20)
	hdr = append(hdr, []byte(snapshotMagic)...)
	var b8 [8]byte
	binary.BigEndian.PutUint64(b8[:], uint64(meta.Generation))
	hdr = append(hdr, b8[:]...)
	binary.BigEndian.PutUint64(b8[:], meta.LSN)
	hdr = append(hdr, b8[:]...)
	var b4 [4]byte
	binary.BigEndian.PutUint32(b4[:], uint32(len(pages)))
	hdr = append(hdr, b4[:]...)
	if _, err := w.Write(hdr); err != nil {
		f.Close()
		return err
	}
	// Deterministic page order keeps snapshot files reproducible.
	ids := make([]int, 0, len(pages))
	for id := range pages {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	for _, i := range ids {
		id := uint32(i)
		sp := pages[id]
		binary.BigEndian.PutUint32(b4[:], id)
		if _, err := w.Write(b4[:]); err != nil {
			f.Close()
			return err
		}
		binary.BigEndian.PutUint64(b8[:], sp.PageLSN)
		if _, err := w.Write(b8[:]); err != nil {
			f.Close()
			return err
		}
		binary.BigEndian.PutUint32(b4[:], uint32(len(sp.Data)))
		if _, err := w.Write(b4[:]); err != nil {
			f.Close()
			return err
		}
		if _, err := w.Write(sp.Data); err != nil {
			f.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readSnapshotFile(path string) (map[uint32]SnapshotPage, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rd := bytes.NewReader(data)
	magic := make([]byte, 8)
	if _, err := io.ReadFull(rd, magic); err != nil || string(magic) != snapshotMagic {
		return nil, fmt.Errorf("snapshot: %s: bad magic", path)
	}
	var b8 [8]byte
	var b4 [4]byte
	io.ReadFull(rd, b8[:]) // generation (in manifest already)
	io.ReadFull(rd, b8[:]) // lsn
	io.ReadFull(rd, b4[:])
	count := binary.BigEndian.Uint32(b4[:])
	out := make(map[uint32]SnapshotPage, count)
	for i := uint32(0); i < count; i++ {
		if _, err := io.ReadFull(rd, b4[:]); err != nil {
			return nil, fmt.Errorf("snapshot: %s: truncated page header", path)
		}
		id := binary.BigEndian.Uint32(b4[:])
		io.ReadFull(rd, b8[:])
		pageLSN := binary.BigEndian.Uint64(b8[:])
		io.ReadFull(rd, b4[:])
		n := binary.BigEndian.Uint32(b4[:])
		pageData := make([]byte, n)
		if _, err := io.ReadFull(rd, pageData); err != nil {
			return nil, fmt.Errorf("snapshot: %s: truncated page %d data", path, id)
		}
		out[id] = SnapshotPage{PageLSN: pageLSN, Data: pageData}
	}
	return out, nil
}

func readManifest(dir string) (*manifest, error) {
	data, err := ioutil.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("snapshot: corrupt manifest: %v", err)
	}
	return &m, nil
}

func writeManifest(dir string, m *manifest) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "manifest.json.tmp")
	if err := ioutil.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "manifest.json"))
}
