package lfs

import (
	"sort"
	"sync"
)

// Segment is the in-memory bookkeeping for one fixed-size log segment.
type Segment struct {
	ID     int
	Active bool // one of the two currently appendable (hot/cold) segments
	Hot    bool // temperature class when Active
	// Summary reverse pointers, indexed by slot; nil slots hold a zero value.
	Summary []Summary
	// WriteTimeNs[slot] is when each block was logged; age derives from it.
	WriteTimeNs []int64
}

// Snapshot is an explicit named snapshot pinning a whole NAT generation.
type Snapshot struct {
	Name string
	// Mapping maps each logical block to the physical version pinned at create.
	Mapping map[NATKey]NATEntry
}

// Config configures a filesystem.
type Config struct {
	// SegmentCount is the total number of segments on the device.
	SegmentCount int
	// BlocksPerSeg is the fixed block count B per segment.
	BlocksPerSeg int
	// EmergencyWatermark: when free segments fall below it the cleaner
	// guarantees forward progress instead of deadlocking.
	EmergencyWatermark int
	// Clock is injectable; defaults to the wall clock.
	Clock Clock
}

// FS is the log-structured storage layer.
type FS struct {
	mu      sync.Mutex
	dev     *Device
	blocks  int
	clock   Clock
	emergWm int

	segments []*Segment
	freeSegs []int // stack of never-used/reclaimed segment ids
	// reserve holds up to EmergencyWatermark segments that normal append
	// allocation must never consume. They are reserved exclusively as migration
	// targets, guaranteeing the cleaner can always relocate live blocks and
	// therefore never deadlocks even when the device is otherwise full.
	reserve   []int
	hotCur    int // current hot append segment, -1 if none open
	coldCur   int // current cold append segment, -1 if none open
	nextSeq   uint64
	nextVer   uint64
	cpSlot    int // slot written by the next SaveCheckpoint
	cpVersion uint64
	cpLastSeq uint64 // highest sequence covered by the latest checkpoint

	nat      map[NATKey]NATEntry
	snaps    map[string]*Snapshot
	lastSync map[NodeID]uint64 // inode -> fsync marker sequence (in-memory)
}

// NewFS formats a fresh filesystem on a new device.
func NewFS(cfg Config) *FS {
	if cfg.SegmentCount <= 0 || cfg.BlocksPerSeg <= 0 {
		panic("lfs: invalid geometry")
	}
	clock := cfg.Clock
	if clock == nil {
		clock = WallClock
	}
	wm := cfg.EmergencyWatermark
	if wm < 0 {
		wm = 0
	}
	dev := NewDevice(cfg.SegmentCount, cfg.BlocksPerSeg)
	fs := &FS{
		dev:      dev,
		blocks:   cfg.BlocksPerSeg,
		clock:    clock,
		emergWm:  wm,
		segments: make([]*Segment, cfg.SegmentCount),
		hotCur:   -1,
		coldCur:  -1,
		nextSeq:  1,
		nextVer:  1,
		cpSlot:   0,
		nat:      map[NATKey]NATEntry{},
		snaps:    map[string]*Snapshot{},
		lastSync: map[NodeID]uint64{},
	}
	for id := 0; id < cfg.SegmentCount; id++ {
		fs.segments[id] = fs.newSegment(id)
		fs.depositFreed(id)
	}
	return fs
}

func (fs *FS) newSegment(id int) *Segment {
	return &Segment{
		ID:          id,
		Summary:     make([]Summary, fs.blocks),
		WriteTimeNs: make([]int64, fs.blocks),
	}
}

// Device exposes the raw device so tests can crash and recover it.
func (fs *FS) Device() *Device { return fs.dev }

// ---- allocation ----

// popFree returns a free segment id, cleaning first when the free pool is
// exhausted so append-only writes never deadlock.
func (fs *FS) popFree() (int, error) {
	if len(fs.freeSegs) == 0 {
		if err := fs.reserveSpaceLocked(); err != nil {
			return 0, err
		}
	}
	// The emergency reserve must remain untouched by normal allocations.
	if len(fs.freeSegs) == 0 {
		return 0, ErrNoSpace
	}
	last := len(fs.freeSegs) - 1
	id := fs.freeSegs[last]
	fs.freeSegs = fs.freeSegs[:last]
	return id, nil
}

// reserveSpaceLocked guarantees at least one free segment without recursing
// into migration: it reclaims empty or fully-dead non-active segments, which
// require no target space and therefore cannot deadlock.
func (fs *FS) reserveSpaceLocked() error {
	stats := CleanStats{Victim: -1}
	fs.reclaimEmptyLocked(&stats)
	if len(fs.freeSegs) > 0 {
		return nil
	}
	candidates := fs.rankCandidatesLocked()
	for _, c := range candidates {
		if c.Live == 0 {
			fs.dev.resetSegment(c.SegID)
			fs.segments[c.SegID] = fs.newSegment(c.SegID)
			fs.depositFreed(c.SegID)
			return nil
		}
	}
	return ErrNoSpace
}

// popFreeForMigration allocates a target segment during cleaning without
// triggering another cleaning pass. It is the only path allowed to draw from
// the emergency reserve, which is what prevents cleaner deadlock.
func (fs *FS) popFreeForMigration() (int, error) {
	if len(fs.freeSegs) > 0 {
		last := len(fs.freeSegs) - 1
		id := fs.freeSegs[last]
		fs.freeSegs = fs.freeSegs[:last]
		return id, nil
	}
	if len(fs.reserve) > 0 {
		last := len(fs.reserve) - 1
		id := fs.reserve[last]
		fs.reserve = fs.reserve[:last]
		return id, nil
	}
	return 0, ErrNoSpace
}

// depositFreed routes a reclaimed segment into the reserve while the reserve
// is below the emergency watermark, otherwise into the normal free pool.
func (fs *FS) depositFreed(id int) {
	if fs.inPool(id) {
		return // never double-list a segment
	}
	if len(fs.reserve) < fs.emergWm {
		fs.reserve = append(fs.reserve, id)
		return
	}
	fs.freeSegs = append(fs.freeSegs, id)
}

// inPool reports whether id is currently in either allocation pool.
func (fs *FS) inPool(id int) bool {
	for _, x := range fs.freeSegs {
		if x == id {
			return true
		}
	}
	for _, x := range fs.reserve {
		if x == id {
			return true
		}
	}
	return false
}

// openSegment returns the current append segment for a temperature class,
// opening a fresh one when it is absent or full.
func (fs *FS) openSegment(hot, forMigration bool) (int, error) {
	cur := fs.coldCur
	if hot {
		cur = fs.hotCur
	}
	if cur >= 0 && fs.usedSlots(fs.segments[cur]) < fs.blocks {
		return cur, nil
	}
	var id int
	var err error
	if forMigration {
		id, err = fs.popFreeForMigration()
	} else {
		id, err = fs.popFree()
	}
	if err != nil {
		return 0, err
	}
	seg := fs.segments[id]
	seg.Active = true
	seg.Hot = hot
	if hot {
		fs.hotCur = id
	} else {
		fs.coldCur = id
	}
	return id, nil
}

func (fs *FS) usedSlots(seg *Segment) int {
	n := 0
	for slot := 0; slot < fs.blocks; slot++ {
		if fs.dev.BlockOccupied(PhysAddr{seg.ID, slot}) {
			n++
		}
	}
	return n
}

// allocBlock opens the right class segment and reserves the next free slot.
func (fs *FS) allocBlock(hot bool) (PhysAddr, error) {
	return fs.allocBlockMode(hot, false)
}

func (fs *FS) allocBlockMode(hot, forMigration bool) (PhysAddr, error) {
	segID, err := fs.openSegment(hot, forMigration)
	if err != nil {
		return PhysAddr{}, err
	}
	for slot := 0; slot < fs.blocks; slot++ {
		addr := PhysAddr{segID, slot}
		if !fs.dev.BlockOccupied(addr) {
			return addr, nil
		}
	}
	return PhysAddr{}, ErrNoSpace
}

func (fs *FS) sealIfFull(segID int) {
	seg := fs.segments[segID]
	if fs.usedSlots(seg) >= fs.blocks {
		seg.Active = false
		if fs.hotCur == segID {
			fs.hotCur = -1
		}
		if fs.coldCur == segID {
			fs.coldCur = -1
		}
	}
}

// ---- data path ----

// Write appends a new version of logical block (ino, blkIdx). hot selects the
// hot or cold append stream; the NAT is repointed atomically and the old
// physical version becomes garbage unless a snapshot still pins it.
func (fs *FS) Write(ino NodeID, blkIdx BlockIndex, data []byte, hot bool) (PhysAddr, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if ino == 0 {
		return PhysAddr{}, ErrInvalidArgument
	}
	addr, err := fs.allocBlock(hot)
	if err != nil {
		return PhysAddr{}, err
	}
	ver := fs.nextVer
	fs.nextVer++
	seq := fs.nextSeq
	fs.nextSeq++
	now := fs.clock()

	node := Node{
		Kind:   KindData,
		Seq:    seq,
		Ver:    ver,
		Ino:    ino,
		BlkIdx: blkIdx,
		Hot:    hot,
		TimeNs: now,
		Data:   append([]byte(nil), data...),
	}
	if err := fs.dev.WriteNode(addr, node); err != nil {
		return PhysAddr{}, err
	}
	seg := fs.segments[addr.Seg]
	seg.Summary[addr.Slot] = Summary{Ino: ino, BlkIdx: blkIdx, Ver: ver, Hot: hot}
	seg.WriteTimeNs[addr.Slot] = now
	fs.nat[NATKey{ino, blkIdx}] = NATEntry{Addr: addr, Ver: ver, Hot: hot}
	fs.sealIfFull(addr.Seg)
	return addr, nil
}

// Fsync logs an fsync commit marker for ino, closing a durable node chain.
func (fs *FS) Fsync(ino NodeID) (PhysAddr, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.fsyncMode(ino, false)
}

// fsyncLocked appends an fsync marker; caller must hold fs.mu.
func (fs *FS) fsyncLocked(ino NodeID) (PhysAddr, error) {
	return fs.fsyncMode(ino, false)
}

func (fs *FS) fsyncMode(ino NodeID, forMigration bool) (PhysAddr, error) {
	if ino == 0 {
		return PhysAddr{}, ErrInvalidArgument
	}
	addr, err := fs.allocBlockMode(true, forMigration)
	if err != nil {
		return PhysAddr{}, err
	}
	seq := fs.nextSeq
	fs.nextSeq++
	now := fs.clock()
	node := Node{
		Kind:   KindFsync,
		Seq:    seq,
		Ino:    ino,
		Fsync:  true,
		Hot:    true,
		TimeNs: now,
	}
	if err := fs.dev.WriteNode(addr, node); err != nil {
		return PhysAddr{}, err
	}
	seg := fs.segments[addr.Seg]
	seg.Summary[addr.Slot] = Summary{Ino: ino, Ver: 0, Hot: true}
	seg.WriteTimeNs[addr.Slot] = now
	fs.lastSync[ino] = seq
	fs.sealIfFull(addr.Seg)
	return addr, nil
}

// Read returns the current logical block content and its physical address.
func (fs *FS) Read(ino NodeID, blkIdx BlockIndex) ([]byte, PhysAddr, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	entry, ok := fs.nat[NATKey{ino, blkIdx}]
	if !ok {
		return nil, PhysAddr{}, ErrInvalidNode
	}
	node, err := fs.dev.ReadNode(entry.Addr)
	if err != nil {
		return nil, PhysAddr{}, err
	}
	return append([]byte(nil), node.Data...), entry.Addr, nil
}

// NATEntryOf returns the current NAT mapping for a logical block.
func (fs *FS) NATEntryOf(ino NodeID, blkIdx BlockIndex) (NATEntry, bool) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	e, ok := fs.nat[NATKey{ino, blkIdx}]
	return e, ok
}

// ---- liveness ----

// IsLive precisely decides whether a physical block is still alive. A block
// is alive iff the current NAT still points at it OR an active snapshot
// references it. Fsync marker blocks are never referenced by NAT/snapshots and
// are therefore reclaimable metadata.
func (fs *FS) IsLive(addr PhysAddr) bool {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.isLiveLocked(addr)
}

func (fs *FS) isLiveLocked(addr PhysAddr) bool {
	for _, entry := range fs.nat {
		if entry.Addr == addr {
			return true
		}
	}
	for _, snap := range fs.snaps {
		for _, entry := range snap.Mapping {
			if entry.Addr == addr {
				return true
			}
		}
	}
	return false
}

// liveAddrs builds the set of all currently live physical addresses.
func (fs *FS) liveAddrs() map[PhysAddr]bool {
	live := make(map[PhysAddr]bool, len(fs.nat))
	for _, entry := range fs.nat {
		live[entry.Addr] = true
	}
	for _, snap := range fs.snaps {
		for _, entry := range snap.Mapping {
			live[entry.Addr] = true
		}
	}
	return live
}

// segmentAge returns the age of the segment's youngest block: now minus the
// maximum write timestamp among its occupied slots.
func (fs *FS) segmentAge(seg *Segment) int64 {
	var youngest int64 = -1
	for slot := 0; slot < fs.blocks; slot++ {
		if fs.dev.BlockOccupied(PhysAddr{seg.ID, slot}) {
			if t := seg.WriteTimeNs[slot]; t > youngest {
				youngest = t
			}
		}
	}
	if youngest < 0 {
		return 0
	}
	age := fs.clock() - youngest
	if age < 0 {
		age = 0
	}
	return age
}

// ---- accessors for tests / cleaning ----

// FreeSegmentCount returns the size of the free segment pool.
func (fs *FS) FreeSegmentCount() int {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return len(fs.freeSegs) + len(fs.reserve)
}

// ReserveCount reports the number of segments held in the emergency reserve.
func (fs *FS) ReserveCount() int {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return len(fs.reserve)
}

// SegmentCount returns the device segment count.
func (fs *FS) SegmentCount() int { return len(fs.segments) }

// BlocksPerSegment returns B.
func (fs *FS) BlocksPerSegment() int { return fs.blocks }

// SnapshotNames returns the active snapshot names in sorted order.
func (fs *FS) SnapshotNames() []string {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	names := make([]string, 0, len(fs.snaps))
	for name := range fs.snaps {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// segmentOf returns the bookkeeping object for a segment.
func (fs *FS) segmentOf(id int) *Segment { return fs.segments[id] }
