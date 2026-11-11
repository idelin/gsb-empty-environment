package netsec

import "sync"

// Op describes the kind of filesystem mutation carried by an Event.
type Op int

const (
	OpCreate Op = iota
	OpWrite
	OpRemove
	OpRename
)

// Event is a single notification from the kernel file-event stream.
// Seq is the stream-wide monotonically increasing sequence number.
// ObjectID is the identity of the affected filesystem object (inode);
// events that only know a path must leave ObjectID zero.
type Event struct {
	Seq      uint64
	Op       Op
	Path     string
	OldPath  string // rename source, only for OpRename
	ObjectID uint64
}

// Entry is one file observed by a full scan.
type Entry struct {
	Path     string
	ObjectID uint64
}

// FileState is the monitor's knowledge about one path.
type FileState struct {
	ObjectID uint64
	Dirty    bool
}

// Monitor merges a full-scan snapshot with the event stream.
//
// Recovery model: a scan started with BeginScan records a barrier equal
// to the highest applied event sequence. Events with Seq > barrier are
// deltas that happened during the scan; they are buffered and replayed
// onto the snapshot when CompleteScan succeeds. A queue overflow at any
// time revokes the clean verdict; an overflow after the barrier
// invalidates the in-flight scan, which must be retried.
type Monitor struct {
	mu sync.Mutex

	files   map[string]*FileState
	objects map[uint64]map[string]struct{} // objectID -> paths (hardlinks)

	lastSeq uint64

	scanning    bool
	scanBarrier uint64
	pending     []Event // events with Seq > scanBarrier, replayed on completion

	overflowSeq uint64 // highest stream position known to have lost events
	lost        bool   // overflow not yet recovered by a completed scan
}

func NewMonitor() *Monitor {
	return &Monitor{
		files:   make(map[string]*FileState),
		objects: make(map[uint64]map[string]struct{}),
	}
}

// HandleEvent applies one stream event. Events arriving while a scan is
// in progress are also buffered for replay onto the scan snapshot.
func (m *Monitor) HandleEvent(ev Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ev.Seq <= m.lastSeq {
		return // duplicate or out-of-order redelivery
	}
	m.lastSeq = ev.Seq
	m.applyLocked(ev)
	if m.scanning && ev.Seq > m.scanBarrier {
		m.pending = append(m.pending, ev)
	}
}

// HandleOverflow records a kernel queue overflow at stream position seq.
// Any clean verdict is revoked immediately; if a scan is in flight it
// will be rejected by CompleteScan.
func (m *Monitor) HandleOverflow(seq uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lost = true
	if seq > m.overflowSeq {
		m.overflowSeq = seq
	}
}

// BeginScan starts a recovery scan and returns its barrier sequence.
// Events with Seq <= barrier are covered by the full walk; later events
// are merged as deltas. Losses before the barrier are covered by the
// walk itself, so prior overflow bookkeeping is reset.
func (m *Monitor) BeginScan() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scanning = true
	m.scanBarrier = m.lastSeq
	m.overflowSeq = 0
	m.pending = nil
	return m.scanBarrier
}

// CompleteScan installs the snapshot gathered since barrier and replays
// the buffered post-barrier deltas on top of it. It returns false when
// events were lost after the barrier (overflow during the scan); the
// caller must discard the snapshot and start a new scan.
func (m *Monitor) CompleteScan(barrier uint64, entries []Entry) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.scanning || barrier != m.scanBarrier {
		return false
	}
	m.scanning = false
	if m.overflowSeq > barrier {
		m.pending = nil
		return false
	}
	m.files = make(map[string]*FileState, len(entries))
	m.objects = make(map[uint64]map[string]struct{}, len(entries))
	for _, e := range entries {
		m.files[e.Path] = &FileState{ObjectID: e.ObjectID}
		m.linkLocked(e.ObjectID, e.Path)
	}
	for _, ev := range m.pending {
		m.applyLocked(ev)
	}
	m.pending = nil
	m.lost = false
	return true
}

// Lost reports whether an overflow has not yet been recovered.
func (m *Monitor) Lost() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lost
}

// Clean reports whether the directory can be declared unchanged: no
// unrecovered overflow and no dirty path.
func (m *Monitor) Clean() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.lost {
		return false
	}
	for _, f := range m.files {
		if f.Dirty {
			return false
		}
	}
	return true
}

// Dirty reports whether the given path is known to have changed.
func (m *Monitor) Dirty(path string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[path]
	return ok && f.Dirty
}

// State returns a copy of the file state at path.
func (m *Monitor) State(path string) (FileState, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f, ok := m.files[path]
	if !ok {
		return FileState{}, false
	}
	return *f, true
}

// Paths returns the paths currently bound to objectID (hardlink set).
func (m *Monitor) Paths(objectID uint64) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for p := range m.objects[objectID] {
		out = append(out, p)
	}
	return out
}

func (m *Monitor) applyLocked(ev Event) {
	switch ev.Op {
	case OpCreate, OpWrite:
		m.applyWriteLocked(ev)
	case OpRemove:
		m.applyRemoveLocked(ev)
	case OpRename:
		m.applyRenameLocked(ev)
	}
}

func (m *Monitor) applyWriteLocked(ev Event) {
	if ev.ObjectID == 0 {
		if f, ok := m.files[ev.Path]; ok {
			f.Dirty = true
		} else {
			m.files[ev.Path] = &FileState{Dirty: true}
		}
		return
	}
	if f, ok := m.files[ev.Path]; !ok || f.ObjectID != ev.ObjectID {
		if ok {
			m.unlinkLocked(f.ObjectID, ev.Path)
		}
		m.files[ev.Path] = &FileState{ObjectID: ev.ObjectID}
		m.linkLocked(ev.ObjectID, ev.Path)
	}
	// A write touches the object; every hardlinked name is dirty.
	for p := range m.objects[ev.ObjectID] {
		if f, ok := m.files[p]; ok {
			f.Dirty = true
		}
	}
}

func (m *Monitor) applyRemoveLocked(ev Event) {
	f, ok := m.files[ev.Path]
	if !ok {
		return
	}
	// Bind to the object: a stale remove must not delete a different
	// object that now occupies the same path.
	if ev.ObjectID != 0 && f.ObjectID != ev.ObjectID {
		return
	}
	m.unlinkLocked(f.ObjectID, ev.Path)
	delete(m.files, ev.Path)
}

func (m *Monitor) applyRenameLocked(ev Event) {
	if old, ok := m.files[ev.OldPath]; ok && old.ObjectID == ev.ObjectID {
		// The rename source still holds the same object: move it.
		m.unlinkLocked(old.ObjectID, ev.OldPath)
		delete(m.files, ev.OldPath)
	}
	// If the source path now holds a different object (name reused
	// during the scan), leave it untouched.
	if cur, ok := m.files[ev.Path]; ok && cur.ObjectID != ev.ObjectID {
		m.unlinkLocked(cur.ObjectID, ev.Path)
		delete(m.files, ev.Path)
	}
	m.files[ev.Path] = &FileState{ObjectID: ev.ObjectID, Dirty: true}
	m.linkLocked(ev.ObjectID, ev.Path)
}

func (m *Monitor) linkLocked(objectID uint64, path string) {
	if objectID == 0 {
		return
	}
	set, ok := m.objects[objectID]
	if !ok {
		set = make(map[string]struct{})
		m.objects[objectID] = set
	}
	set[path] = struct{}{}
}

func (m *Monitor) unlinkLocked(objectID uint64, path string) {
	set, ok := m.objects[objectID]
	if !ok {
		return
	}
	delete(set, path)
	if len(set) == 0 {
		delete(m.objects, objectID)
	}
}
