package cachemodel

import (
	"sort"
	"sync"
	"testing"
)

// byteModel is a per-byte version oracle. It tracks, for every byte of the
// virtual device:
//   - cacheVer: the version of the most recent cache write covering the byte
//   - storeVer: the version of the write whose value the store currently holds
//   - history:  every write's payload, keyed by (page, version)
//
// Invariants it enforces:
//   1. store versions never go backwards (a stale writeback can never
//      clobber newer data already persisted).
//   2. every byte the store receives equals the payload of some write with
//      version in [storeVer, cacheVer] — i.e. a value the cache really held.
//   3. at quiescent points (no writeback in flight, no concurrent writers)
//      the cache's dirty set equals exactly {b : cacheVer[b] > storeVer[b]}.
//   4. resident page data equals the payload of cacheVer for every byte.
type byteModel struct {
	t        *testing.T
	pageSize int

	mu        sync.Mutex
	cacheVer  []uint64
	storeVer  []uint64
	storeData []byte
	writes    map[verKey]writeRec
	perByte   [][]bwEntry // per global byte: (version, value) history, sorted by version
}

type verKey struct {
	page int
	ver  uint64
}

type writeRec struct {
	off  int
	data []byte
}

type bwEntry struct {
	ver uint64
	val byte
}

func newByteModel(t *testing.T, pageSize, npages int) *byteModel {
	size := pageSize * npages
	return &byteModel{
		t:         t,
		pageSize:  pageSize,
		writes:    make(map[verKey]writeRec),
		cacheVer:  make([]uint64, size),
		storeVer:  make([]uint64, size),
		storeData: make([]byte, size),
		perByte:   make([][]bwEntry, size),
	}
}

func (m *byteModel) failf(format string, args ...interface{}) {
	m.t.Errorf(format, args...)
}

// hook is installed as Cache.WriteHook; it runs under the page lock, so any
// writeback snapshot that can observe version v happens after this records v.
func (m *byteModel) hook(page, off int, data []byte, ver uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.writes[verKey{page, ver}] = writeRec{off: off, data: cp}
	base := page * m.pageSize
	for i, b := range cp {
		g := base + off + i
		if ver <= m.cacheVer[g] {
			m.failf("byte %d: write version %d not increasing (last %d)", g, ver, m.cacheVer[g])
			return
		}
		m.cacheVer[g] = ver
		m.perByte[g] = append(m.perByte[g], bwEntry{ver: ver, val: b})
	}
}

// observeWrite checks and records bytes accepted by the store.
// Version attribution is ambiguous when two writes to the same byte carry
// the same value, so we attribute the largest matching version <= cacheVer;
// any future store write of the byte then has a true version >= that
// (writes covering the byte only move forward), keeping monotonicity sound.
func (m *byteModel) observeWrite(off int64, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, b := range data {
		g := int(off) + i
		hist := m.perByte[g]
		// Last history entry with ver <= cacheVer[g].
		hi := sort.Search(len(hist), func(j int) bool { return hist[j].ver > m.cacheVer[g] })
		found := false
		for j := hi - 1; j >= 0; j-- {
			if hist[j].val == b {
				if hist[j].ver < m.storeVer[g] {
					m.failf("byte %d: store moved backwards: value %#x matches version %d < storeVer %d",
						g, b, hist[j].ver, m.storeVer[g])
					break
				}
				m.storeVer[g] = hist[j].ver
				m.storeData[g] = b
				found = true
				break
			}
		}
		if !found {
			m.failf("byte %d: store accepted value %#x matching no write version in [%d, %d]",
				g, b, m.storeVer[g], m.cacheVer[g])
		}
	}
}

// lastVal returns the value the most recent cache write assigned to byte g.
func (m *byteModel) lastVal(g int) byte {
	hist := m.perByte[g]
	if len(hist) == 0 {
		return 0
	}
	return hist[len(hist)-1].val
}

// checkQuiescent verifies invariants 3 and 4. Call only when no writeback
// is in flight and no writer is running.
func (m *byteModel) checkQuiescent(c *Cache, npages int) {
	type snap struct {
		data     []byte
		dirty    []bool
		resident bool
	}
	snaps := make([]snap, npages)
	for pg := 0; pg < npages; pg++ {
		d, bm, ok := c.snapshotPage(pg)
		snaps[pg] = snap{d, bm, ok}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for pg := 0; pg < npages; pg++ {
		s := snaps[pg]
		for i := 0; i < m.pageSize; i++ {
			g := pg*m.pageSize + i
			wantDirty := m.cacheVer[g] > m.storeVer[g]
			if !s.resident {
				if wantDirty {
					m.failf("byte %d: page %d not resident but cacheVer %d > storeVer %d",
						g, pg, m.cacheVer[g], m.storeVer[g])
				}
				continue
			}
			if s.dirty[i] != wantDirty {
				m.failf("byte %d: dirty=%v, want %v (cacheVer %d, storeVer %d)",
					g, s.dirty[i], wantDirty, m.cacheVer[g], m.storeVer[g])
			}
			if m.cacheVer[g] > 0 && s.data[i] != m.lastVal(g) {
				m.failf("byte %d: page data %#x, want %#x (version %d)",
					g, s.data[i], m.lastVal(g), m.cacheVer[g])
			}
		}
	}
}

// checkConverged verifies that after a full flush the store holds exactly
// the latest value of every byte ever written.
func (m *byteModel) checkConverged(c *Cache, npages int, storeBytes []byte) {
	type snap struct {
		dirty    []bool
		resident bool
	}
	snaps := make([]snap, npages)
	for pg := 0; pg < npages; pg++ {
		_, d, ok := c.snapshotPage(pg)
		snaps[pg] = snap{d, ok}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for pg := 0; pg < npages; pg++ {
		if snaps[pg].resident {
			for i, d := range snaps[pg].dirty {
				if d {
					m.failf("page %d byte %d still dirty after flush", pg, i)
				}
			}
		}
	}
	for g := 0; g < len(m.cacheVer); g++ {
		if m.storeVer[g] != m.cacheVer[g] {
			m.failf("byte %d: storeVer %d != cacheVer %d after flush", g, m.storeVer[g], m.cacheVer[g])
		}
		if storeBytes[g] != m.lastVal(g) {
			m.failf("byte %d: store holds %#x, want latest written value %#x",
				g, storeBytes[g], m.lastVal(g))
		}
	}
}

// modelStore wraps a Store and reports every acked byte to the oracle.
type modelStore struct {
	inner Store
	m     *byteModel
}

func (s *modelStore) WriteAt(p []byte, off int64) (int, error) {
	n, err := s.inner.WriteAt(p, off)
	if n > 0 {
		s.m.observeWrite(off, p[:n])
	}
	return n, err
}

func (s *modelStore) ReadAt(p []byte, off int64) (int, error) {
	return s.inner.ReadAt(p, off)
}
