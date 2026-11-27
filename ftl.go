package ftl

import (
	"fmt"
	"sort"
	"sync"
)

// PPA identifies a physical page inside a physical erase block.
type PPA struct {
	Block int
	Page  int
}

// LBA is the logical page address type.
type LBA = int

// Seq is the monotonic write-sequence number type stored in OOB metadata.
type Seq = uint64

func (p PPA) String() string { return fmt.Sprintf("(%d,%d)", p.Block, p.Page) }

// PageState is the externally visible state of a physical page.
type PageState uint8

const (
	// PageFree means the slot has not been programmed since the last erase.
	PageFree PageState = iota
	// PageValid means the slot is the current target of its LBA.
	PageValid
	// PageInvalid means the slot holds a complete but superseded/TRIMmed page.
	PageInvalid
	// PageTorn means the slot was programmed but its OOB checksum is invalid.
	PageTorn
)

func (s PageState) String() string {
	switch s {
	case PageFree:
		return "free"
	case PageValid:
		return "valid"
	case PageInvalid:
		return "invalid"
	case PageTorn:
		return "torn"
	default:
		return "unknown"
	}
}

// ProgramInfo describes a page program about to happen.  It is supplied to a
// fault hook so tests can tear a specific user write or a GC migration step.
type ProgramInfo struct {
	Kind string
	LBA  int
	Seq  uint64
	PPA  PPA
	GC   bool
}

// ProgramFault describes an injected page-program failure.  When Tear is true
// only DataBytes/OOBBytes reach the medium and recovery will discard the page.
// CorruptCRC can be used for a full write whose stored checksum is broken.
// PowerOff cuts controller power immediately after the program completes, so
// the page is durable but the RAM mapping update is lost.
type ProgramFault struct {
	Tear       bool
	DataBytes  int
	OOBBytes   int
	CorruptCRC bool
	PowerOff   bool
}

// Stats is a cumulative snapshot of FTL activity.
type Stats struct {
	Reads             uint64
	Writes            uint64
	Trims             uint64
	Checkpoints       uint64
	Recoveries        uint64
	GCs               uint64
	GCPages           uint64
	Erases            uint64
	TornWrites        uint64
	JournalRecords    uint64
	RecoveredDataRecs uint64
	RecoveredTrims    uint64
}

// WriteAmplification reports (user pages + GC pages) / user pages.
func (s Stats) WriteAmplification() float64 {
	if s.Writes == 0 {
		return 0
	}
	return float64(s.Writes+s.GCPages) / float64(s.Writes)
}

// Device is a page-level flash translation layer with a durable TRIM journal,
// atomic checkpoints, garbage collection, and power-loss recovery.
type Device struct {
	mu sync.Mutex

	cfg     Config
	flash   *Flash
	meta    *MetaArea
	powered bool

	mapping     map[int]PPA
	mapSeq      map[int]uint64
	tombs       map[int]uint64
	everWritten map[int]struct{}
	valid       [][]bool
	wp          []int
	lastSeq     []uint64
	active      int
	seq         uint64

	stats Stats
	hook  func(ProgramInfo) ProgramFault
}

// New constructs a fresh FTL over a new flash chip and metadata area.
func New(cfg Config) (*Device, error) {
	cfg, err := cfg.normalize()
	if err != nil {
		return nil, err
	}
	flash := newFlashGeometry(cfg.BlockCount, cfg.PagesPerBlock, cfg.PageSize, cfg.OOBSize)
	return newDevice(flash, NewMetaArea(), cfg)
}

// NewDevice is an alias for New.
func NewDevice(cfg Config) (*Device, error) { return New(cfg) }

// NewFTL is an alias for New.
func NewFTL(cfg Config) (*Device, error) { return New(cfg) }

// Open attaches an FTL controller to an existing persistent flash chip and
// metadata area.  Runtime mapping state is reconstructed from the last
// complete checkpoint plus all newer OOB/journal records.
func Open(flash *Flash, meta *MetaArea, cfg Config) (*Device, error) {
	if flash == nil || meta == nil {
		return nil, fmt.Errorf("%w: nil flash or meta area", ErrInvalidConfig)
	}
	cfg, err := cfg.normalize()
	if err != nil {
		return nil, err
	}
	cfg.BlockCount = flash.blockCount
	cfg.PagesPerBlock = flash.pagesPerBlock
	cfg.PageSize = flash.pageSize
	cfg.OOBSize = flash.oobSize
	if cfg.ReservedFreeBlocks >= cfg.BlockCount {
		cfg.ReservedFreeBlocks = cfg.BlockCount - 1
	}
	return newDevice(flash, meta, cfg)
}

func newDevice(flash *Flash, meta *MetaArea, cfg Config) (*Device, error) {
	d := &Device{
		cfg:         cfg,
		flash:       flash,
		meta:        meta,
		powered:     true,
		mapping:     make(map[int]PPA),
		mapSeq:      make(map[int]uint64),
		tombs:       make(map[int]uint64),
		everWritten: make(map[int]struct{}),
		valid:       make([][]bool, cfg.BlockCount),
		wp:          make([]int, cfg.BlockCount),
		lastSeq:     make([]uint64, cfg.BlockCount),
		active:      -1,
	}
	for block := 0; block < cfg.BlockCount; block++ {
		d.valid[block] = make([]bool, cfg.PagesPerBlock)
	}
	if err := d.recoverLocked(); err != nil {
		return nil, err
	}
	// Construction is a boot, not a field-reported recovery.
	d.stats.Recoveries = 0
	return d, nil
}

// Config returns the effective configuration.
func (d *Device) Config() Config { return d.cfg }

// BlockCount returns the number of physical data blocks.
func (d *Device) BlockCount() int { return d.cfg.BlockCount }

// PagesPerBlock returns the number of pages per physical block.
func (d *Device) PagesPerBlock() int { return d.cfg.PagesPerBlock }

// PageSize returns the user-data bytes per page.
func (d *Device) PageSize() int { return d.cfg.PageSize }

// Flash returns the underlying persistent flash chip.
func (d *Device) Flash() *Flash { return d.flash }

// Meta returns the persistent metadata area.
func (d *Device) Meta() *MetaArea { return d.meta }

// Powered reports whether the controller has live RAM state.
func (d *Device) Powered() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.powered
}

// PowerLoss drops all volatile controller state.  The persistent flash and
// metadata are unaffected; call Recover/PowerOn to rebuild.
func (d *Device) PowerLoss() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.powered = false
}

// Crash is an alias for PowerLoss.
func (d *Device) Crash() { d.PowerLoss() }

// Recover rebuilds the mapping table, valid bitmaps, free write pointers, and
// erase accounting from persistent state.
func (d *Device) Recover() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.recoverLocked(); err != nil {
		return err
	}
	d.powered = true
	return nil
}

// PowerOn is an alias for Recover.
func (d *Device) PowerOn() error { return d.Recover() }

// PowerCycle simulates an abrupt power loss followed by a fresh boot.
func (d *Device) PowerCycle() error {
	d.PowerLoss()
	return d.Recover()
}

// Restart is an alias for PowerCycle.
func (d *Device) Restart() error { return d.PowerCycle() }

// Reboot is an alias for PowerCycle.
func (d *Device) Reboot() error { return d.PowerCycle() }

// SetProgramHook installs a deterministic page-program fault injector.
func (d *Device) SetProgramHook(hook func(ProgramInfo) ProgramFault) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.hook = hook
}

// CorruptPage invalidates the OOB checksum of one physical page.  The next
// recovery discards it while still consuming its sequential slot.
func (d *Device) CorruptPage(ppa PPA) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.flash.CorruptPage(ppa.Block, ppa.Page)
}

// Stats returns a cumulative activity snapshot.
func (d *Device) Stats() Stats {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stats
}

func (d *Device) ensurePoweredLocked() error {
	if !d.powered {
		return ErrPoweredOff
	}
	return nil
}

func (d *Device) nextSeqLocked() uint64 {
	d.seq++
	return d.seq
}

// Read returns the current payload for an LBA.  An unmapped or TRIMmed LBA
// deterministically returns a zero-filled page of PageSize bytes.
func (d *Device) Read(lba int) ([]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.ensurePoweredLocked(); err != nil {
		return nil, err
	}
	if lba < 0 {
		return nil, fmt.Errorf("%w: lba=%d", ErrOutOfRange, lba)
	}
	d.stats.Reads++
	ppa, ok := d.mapping[lba]
	if !ok {
		return make([]byte, d.cfg.PageSize), nil
	}
	data, oob, programmed, err := d.flash.ReadRaw(ppa.Block, ppa.Page)
	if err != nil || !programmed {
		return nil, fmt.Errorf("%w: lba=%d ppa=%s", ErrCorruptMetadata, lba, ppa)
	}
	meta, ok := decodePageMeta(oob, data, ppa.Block, ppa.Page, d.cfg.PageSize)
	if !ok || meta.LBA != lba {
		return nil, fmt.Errorf("%w: lba=%d ppa=%s", ErrCorruptMetadata, lba, ppa)
	}
	if meta.Length > len(data) {
		return nil, fmt.Errorf("%w: lba=%d length=%d", ErrCorruptMetadata, lba, meta.Length)
	}
	return append([]byte(nil), data[:meta.Length]...), nil
}

// ReadPage is an alias for Read.
func (d *Device) ReadPage(lba int) ([]byte, error) { return d.Read(lba) }

// ReadLBA is an alias for Read.
func (d *Device) ReadLBA(lba int) ([]byte, error) { return d.Read(lba) }

// ReadInto fills dst with the LBA content, zero-filling any tail.  Unmapped
// LBAs produce all zeroes.  It always reports len(dst) bytes on success.
func (d *Device) ReadInto(lba int, dst []byte) (int, error) {
	value, err := d.Read(lba)
	if err != nil {
		return 0, err
	}
	for i := range dst {
		dst[i] = 0
	}
	copy(dst, value)
	return len(dst), nil
}

// ReadAt is an alias for ReadInto.
func (d *Device) ReadAt(lba int, dst []byte) (int, error) { return d.ReadInto(lba, dst) }

// Write stores data for lba by appending to the active block.  The previous
// physical page, if any, is immediately invalidated.  A successful return
// means the new page and its OOB checksum are durable.
func (d *Device) Write(lba int, data []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.ensurePoweredLocked(); err != nil {
		return err
	}
	if lba < 0 {
		return fmt.Errorf("%w: lba=%d", ErrOutOfRange, lba)
	}
	if len(data) > d.cfg.PageSize {
		return fmt.Errorf("%w: %d > %d", ErrPageTooLarge, len(data), d.cfg.PageSize)
	}
	if err := d.ensureWriteBlockLocked(); err != nil {
		return err
	}

	seq := d.nextSeqLocked()
	block, page := d.active, d.wp[d.active]
	payload := make([]byte, d.cfg.PageSize)
	copy(payload, data)
	if err := d.programDataLocked(block, page, lba, seq, len(data), payload, false); err != nil {
		return err
	}

	old, hadOld := d.mapping[lba]
	d.mapping[lba] = PPA{Block: block, Page: page}
	d.mapSeq[lba] = seq
	d.everWritten[lba] = struct{}{}
	d.valid[block][page] = true
	d.lastSeq[block] = seq
	if hadOld {
		d.valid[old.Block][old.Page] = false
	}
	delete(d.tombs, lba)
	d.stats.Writes++
	return nil
}

// WritePage is an alias for Write.
func (d *Device) WritePage(lba int, data []byte) error { return d.Write(lba, data) }

// WriteAt is an alias for Write.
func (d *Device) WriteAt(lba int, data []byte) error { return d.Write(lba, data) }

// WriteLBA is an alias for Write.
func (d *Device) WriteLBA(lba int, data []byte) error { return d.Write(lba, data) }

// Trim releases [start, start+count).  The durable journal record is appended
// before the mapping is removed, so a crash immediately after a successful
// Trim cannot resurrect an old physical page during OOB recovery.
func (d *Device) Trim(start, count int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.ensurePoweredLocked(); err != nil {
		return err
	}
	if start < 0 || count < 0 {
		return fmt.Errorf("%w: trim start=%d count=%d", ErrOutOfRange, start, count)
	}
	if count == 0 {
		return nil
	}
	if start > int(^uint(0)>>1)-count {
		return fmt.Errorf("%w: trim range overflows", ErrOutOfRange)
	}

	seq := d.nextSeqLocked()
	if err := d.meta.AppendTrim(TrimRecord{Seq: seq, Start: start, Count: count}); err != nil {
		return err
	}
	d.stats.JournalRecords++
	d.applyTrimLocked(start, count, seq)
	d.stats.Trims++
	return nil
}

// applyTrimLocked deletes mappings and installs tombstones only for LBAs that
// have ever held a physical page.  A never-written LBA cannot have a stale OOB
// record to resurrect, so iterating the full range would be wasted work (and
// could be enormous for a discard of the whole namespace).
func (d *Device) applyTrimLocked(start, count int, seq uint64) {
	if count <= 0 {
		return
	}
	end := start + count
	if count > int(^uint(0)>>1)-start {
		end = int(^uint(0) >> 1)
	}
	for lba := range d.mapping {
		if lba >= start && lba < end {
			old := d.mapping[lba]
			d.valid[old.Block][old.Page] = false
			delete(d.mapping, lba)
			delete(d.mapSeq, lba)
		}
	}
	for lba := range d.everWritten {
		if lba < start || lba >= end {
			continue
		}
		if current, ok := d.tombs[lba]; !ok || seq > current {
			d.tombs[lba] = seq
		}
	}
}

// TrimRange is an alias for Trim.
func (d *Device) TrimRange(start, count int) error { return d.Trim(start, count) }

// TrimLBA releases one logical page.
func (d *Device) TrimLBA(lba int) error { return d.Trim(lba, 1) }

// Deallocate is an alias for Trim.
func (d *Device) Deallocate(start, count int) error { return d.Trim(start, count) }

// Discard is an alias for Trim.
func (d *Device) Discard(start, count int) error { return d.Trim(start, count) }

// Unmap is an alias for Trim.
func (d *Device) Unmap(start, count int) error { return d.Trim(start, count) }

// Checkpoint publishes a complete mapping + tombstone snapshot to the
// persistent metadata area.  The journal prefix covered by the snapshot is
// pruned only after the new slot is complete.
func (d *Device) Checkpoint() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.ensurePoweredLocked(); err != nil {
		return err
	}
	seq := d.nextSeqLocked()
	snapshot := checkpointSnapshot{
		Seq:     seq,
		Entries: make([]snapEntry, 0, len(d.mapping)),
		Tombs:   make([]snapTomb, 0, len(d.tombs)),
	}
	for lba, ppa := range d.mapping {
		snapshot.Entries = append(snapshot.Entries, snapEntry{
			LBA: lba, Block: ppa.Block, Page: ppa.Page, Seq: d.mapSeq[lba],
		})
	}
	for lba, seq := range d.tombs {
		snapshot.Tombs = append(snapshot.Tombs, snapTomb{LBA: lba, Seq: seq})
	}
	if err := d.meta.WriteCheckpoint(snapshot); err != nil {
		return err
	}
	d.stats.Checkpoints++
	return nil
}

// Flush is an alias for Checkpoint.
func (d *Device) Flush() error { return d.Checkpoint() }

func (d *Device) programDataLocked(block, page, lba int, seq uint64, length int, payload []byte, gc bool) error {
	oob := encodePageMeta(PageMeta{
		Kind: PageData, LBA: lba, Seq: seq, Length: length,
		Block: block, Page: page,
	}, payload)

	fault := ProgramFault{}
	if d.hook != nil {
		fault = d.hook(ProgramInfo{
			Kind: "data", LBA: lba, Seq: seq,
			PPA: PPA{Block: block, Page: page}, GC: gc,
		})
	}

	var err error
	if fault.Tear {
		dataBytes := fault.DataBytes
		if dataBytes <= 0 || dataBytes >= len(payload) {
			dataBytes = len(payload) / 2
			if dataBytes == 0 {
				dataBytes = 0
			}
		}
		oobBytes := fault.OOBBytes
		if oobBytes <= 0 || oobBytes >= len(oob) {
			oobBytes = len(oob) / 2
		}
		err = d.flash.ProgramPartial(block, page, payload, oob, dataBytes, oobBytes)
		d.stats.TornWrites++
	} else {
		err = d.flash.Program(block, page, payload, oob)
	}
	if err != nil {
		return err
	}
	d.wp[block] = page + 1
	if fault.Tear {
		return ErrProgramTorn
	}
	if fault.CorruptCRC {
		if err := d.flash.InjectOOBFlip(block, page, 20); err != nil {
			return err
		}
		d.stats.TornWrites++
		return ErrProgramTorn
	}
	if fault.PowerOff {
		d.powered = false
		return ErrPoweredOff
	}
	return nil
}

type recoveryEvent struct {
	seq   uint64
	trim  bool
	lba   int
	ppa   PPA
	start int
	count int
}

// recoverLocked discards all volatile state and rebuilds it:
//  1. load the newest fully valid checkpoint (older slot is a fallback),
//  2. scan every physical page's OOB, keeping only checksum-valid records,
//  3. merge data and durable TRIM records in ascending seq order,
//  4. derive valid bitmaps, free write pointers, and the next seq.
func (d *Device) recoverLocked() error {
	d.mapping = make(map[int]PPA)
	d.mapSeq = make(map[int]uint64)
	d.tombs = make(map[int]uint64)
	d.everWritten = make(map[int]struct{})
	for block := 0; block < d.cfg.BlockCount; block++ {
		for page := 0; page < d.cfg.PagesPerBlock; page++ {
			d.valid[block][page] = false
		}
		d.wp[block] = 0
		d.lastSeq[block] = 0
	}
	d.active = -1
	d.seq = 0

	var cutoff uint64
	if snapshot, ok := d.meta.latest(); ok {
		cutoff = snapshot.Seq
		d.seq = snapshot.Seq
		for _, tomb := range snapshot.Tombs {
			if tomb.LBA < 0 || tomb.Seq == 0 {
				continue
			}
			if tomb.Seq > d.tombs[tomb.LBA] {
				d.tombs[tomb.LBA] = tomb.Seq
			}
			if tomb.Seq > d.seq {
				d.seq = tomb.Seq
			}
		}
		for _, entry := range snapshot.Entries {
			if entry.LBA < 0 ||
				entry.Block < 0 || entry.Block >= d.cfg.BlockCount ||
				entry.Page < 0 || entry.Page >= d.cfg.PagesPerBlock {
				continue
			}
			meta, ok := d.flash.PageMeta(entry.Block, entry.Page)
			if !ok || meta.LBA != entry.LBA || meta.Seq != entry.Seq {
				continue
			}
			if tombSeq, trimmed := d.tombs[entry.LBA]; trimmed && tombSeq >= entry.Seq {
				continue
			}
			if existingSeq, exists := d.mapSeq[entry.LBA]; exists && existingSeq >= entry.Seq {
				continue
			}
			d.mapping[entry.LBA] = PPA{Block: entry.Block, Page: entry.Page}
			d.mapSeq[entry.LBA] = entry.Seq
			d.everWritten[entry.LBA] = struct{}{}
			if entry.Seq > d.lastSeq[entry.Block] {
				d.lastSeq[entry.Block] = entry.Seq
			}
		}
	}

	events := make([]recoveryEvent, 0)
	for block := 0; block < d.cfg.BlockCount; block++ {
		for page := 0; page < d.cfg.PagesPerBlock; page++ {
			data, oob, programmed, err := d.flash.ReadRaw(block, page)
			if err != nil {
				return err
			}
			if !programmed {
				if !allZero(data) || !allZero(oob) {
					programmed = true
				}
			}
			if !programmed {
				continue
			}
			if page >= d.wp[block] {
				d.wp[block] = page + 1
			}
			meta, ok := decodePageMeta(oob, data, block, page, d.cfg.PageSize)
			if !ok {
				// A torn/half-written page is physically consumed but never
				// becomes visible to the mapping table.
				continue
			}
			d.everWritten[meta.LBA] = struct{}{}
			if meta.Seq > d.lastSeq[block] {
				d.lastSeq[block] = meta.Seq
			}
			if meta.Seq <= cutoff {
				continue
			}
			events = append(events, recoveryEvent{
				seq: meta.Seq,
				lba: meta.LBA,
				ppa: PPA{Block: block, Page: page},
			})
		}
	}

	for _, record := range d.meta.TrimJournal() {
		if record.Seq <= cutoff {
			continue
		}
		events = append(events, recoveryEvent{
			seq: record.Seq, trim: true, start: record.Start, count: record.Count,
		})
	}

	sort.SliceStable(events, func(i, j int) bool {
		if events[i].seq != events[j].seq {
			return events[i].seq < events[j].seq
		}
		return !events[i].trim && events[j].trim
	})

	d.stats.RecoveredDataRecs = 0
	d.stats.RecoveredTrims = 0
	for _, event := range events {
		if event.seq > d.seq {
			d.seq = event.seq
		}
		if event.trim {
			d.applyTrimLocked(event.start, event.count, event.seq)
			d.stats.RecoveredTrims++
			continue
		}
		if tombSeq, trimmed := d.tombs[event.lba]; trimmed && tombSeq >= event.seq {
			continue
		}
		delete(d.tombs, event.lba)
		d.mapping[event.lba] = event.ppa
		d.mapSeq[event.lba] = event.seq
		d.stats.RecoveredDataRecs++
	}

	for lba, ppa := range d.mapping {
		if ppa.Block < 0 || ppa.Block >= d.cfg.BlockCount ||
			ppa.Page < 0 || ppa.Page >= d.cfg.PagesPerBlock {
			delete(d.mapping, lba)
			delete(d.mapSeq, lba)
			continue
		}
		if tombSeq, trimmed := d.tombs[lba]; trimmed && tombSeq >= d.mapSeq[lba] {
			delete(d.mapping, lba)
			delete(d.mapSeq, lba)
			continue
		}
		d.valid[ppa.Block][ppa.Page] = true
	}

	for block := 0; block < d.cfg.BlockCount; block++ {
		if d.wp[block] < d.cfg.PagesPerBlock {
			if d.active < 0 || d.lastSeq[block] > d.lastSeq[d.active] {
				d.active = block
			}
		}
	}
	d.stats.Recoveries++
	return nil
}

func allZero(buf []byte) bool {
	for _, value := range buf {
		if value != 0 {
			return false
		}
	}
	return true
}

// Lookup returns the current physical mapping of an LBA.
func (d *Device) Lookup(lba int) (PPA, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	ppa, ok := d.mapping[lba]
	return ppa, ok
}

// Mapping returns a copy of the current L2P table.
func (d *Device) Mapping() map[int]PPA {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[int]PPA, len(d.mapping))
	for lba, ppa := range d.mapping {
		out[lba] = ppa
	}
	return out
}

// PageState reports the state of one physical page.
func (d *Device) PageState(ppa PPA) PageState {
	d.mu.Lock()
	defer d.mu.Unlock()
	if ppa.Block < 0 || ppa.Block >= d.cfg.BlockCount ||
		ppa.Page < 0 || ppa.Page >= d.cfg.PagesPerBlock {
		return PageFree
	}
	if !d.flash.Programmed(ppa.Block, ppa.Page) {
		data, oob, _, err := d.flash.ReadRaw(ppa.Block, ppa.Page)
		if err != nil || (allZero(data) && allZero(oob)) {
			return PageFree
		}
	}
	if d.valid[ppa.Block][ppa.Page] {
		return PageValid
	}
	meta, ok := d.flash.PageMeta(ppa.Block, ppa.Page)
	if !ok || meta.Seq == 0 {
		return PageTorn
	}
	return PageInvalid
}

// ValidBitmap returns the per-page valid bitmap of one block.  "Valid" means
// the page is the current L2P target, not merely that its checksum is good.
func (d *Device) ValidBitmap(block int) []bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if block < 0 || block >= d.cfg.BlockCount {
		return nil
	}
	return append([]bool(nil), d.valid[block]...)
}

// ValidPages returns the number of live pages in a block.
func (d *Device) ValidPages(block int) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if block < 0 || block >= d.cfg.BlockCount {
		return 0
	}
	count := 0
	for _, valid := range d.valid[block] {
		if valid {
			count++
		}
	}
	return count
}

// WritePointer returns the reconstructed sequential write pointer of a block.
func (d *Device) WritePointer(block int) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if block < 0 || block >= d.cfg.BlockCount {
		return 0
	}
	return d.wp[block]
}

// ActiveBlock returns the preferred append block, or -1 if none has space.
func (d *Device) ActiveBlock() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.active
}

// NextSeq returns the next write sequence number that will be assigned.
func (d *Device) NextSeq() uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.seq + 1
}

// Tombstones returns a copy of the durable TRIM tombstones.
func (d *Device) Tombstones() map[int]uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[int]uint64, len(d.tombs))
	for lba, seq := range d.tombs {
		out[lba] = seq
	}
	return out
}

// FreeBlocks returns the number of fully erased blocks.
func (d *Device) FreeBlocks() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.freeBlocksLocked()
}

func (d *Device) freeBlocksLocked() int {
	count := 0
	for block := 0; block < d.cfg.BlockCount; block++ {
		if d.wp[block] == 0 {
			count++
		}
	}
	return count
}

// FreePages returns the number of pages that can still be written without an
// erase, including the reserved blocks.
func (d *Device) FreePages() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	total := 0
	for block := 0; block < d.cfg.BlockCount; block++ {
		total += d.cfg.PagesPerBlock - d.wp[block]
	}
	return total
}

// CapacityPages returns the raw physical page count.
func (d *Device) CapacityPages() int { return d.cfg.BlockCount * d.cfg.PagesPerBlock }

// EraseCounts returns a copy of every block's erase counter.
func (d *Device) EraseCounts() []uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	counts := make([]uint64, d.cfg.BlockCount)
	for block := range counts {
		counts[block] = d.flash.EraseCount(block)
	}
	return counts
}

// EraseCount returns one block's erase counter.
func (d *Device) EraseCount(block int) uint64 { return d.flash.EraseCount(block) }

// WearDelta returns the erase-count spread across blocks that have ever held
// data (a block is included once it is programmed or has been erased by GC).
func (d *Device) WearDelta() uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.wearDeltaLocked()
}

func (d *Device) wearDeltaLocked() uint64 {
	var min, max uint64
	seen := false
	for block := 0; block < d.cfg.BlockCount; block++ {
		count := d.flash.EraseCount(block)
		if d.wp[block] == 0 && count == 0 {
			continue
		}
		if !seen {
			min, max, seen = count, count, true
			continue
		}
		if count < min {
			min = count
		}
		if count > max {
			max = count
		}
	}
	if !seen {
		return 0
	}
	return max - min
}

// Verify checks the physical-page conservation invariants.  It is intended for
// tests and firmware self-checks.
func (d *Device) Verify() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	seen := make(map[PPA]int, len(d.mapping))
	for lba, ppa := range d.mapping {
		if ppa.Block < 0 || ppa.Block >= d.cfg.BlockCount ||
			ppa.Page < 0 || ppa.Page >= d.cfg.PagesPerBlock {
			return fmt.Errorf("lba %d maps out of range ppa %s", lba, ppa)
		}
		if other, exists := seen[ppa]; exists {
			return fmt.Errorf("lba %d and %d share ppa %s", lba, other, ppa)
		}
		seen[ppa] = lba
		if !d.valid[ppa.Block][ppa.Page] {
			return fmt.Errorf("lba %d ppa %s is mapped but not valid", lba, ppa)
		}
		meta, ok := d.flash.PageMeta(ppa.Block, ppa.Page)
		if !ok {
			return fmt.Errorf("lba %d ppa %s has invalid OOB", lba, ppa)
		}
		if meta.LBA != lba || meta.Seq != d.mapSeq[lba] {
			return fmt.Errorf("lba %d ppa %s metadata mismatch lba=%d seq=%d", lba, ppa, meta.LBA, meta.Seq)
		}
	}

	validCount := 0
	for block := 0; block < d.cfg.BlockCount; block++ {
		if d.wp[block] < 0 || d.wp[block] > d.cfg.PagesPerBlock {
			return fmt.Errorf("block %d bad write pointer %d", block, d.wp[block])
		}
		if got := d.flash.WritePointer(block); got != d.wp[block] {
			return fmt.Errorf("block %d write pointer mismatch flash=%d runtime=%d", block, got, d.wp[block])
		}
		for page := 0; page < d.cfg.PagesPerBlock; page++ {
			state := d.pageStateLocked(PPA{Block: block, Page: page})
			if page < d.wp[block] {
				if state == PageFree {
					return fmt.Errorf("block %d page %d is below write pointer but free", block, page)
				}
			} else if state != PageFree {
				return fmt.Errorf("block %d page %d is above write pointer but %s", block, page, state)
			}
			if state == PageValid {
				validCount++
				if lba, ok := seen[PPA{Block: block, Page: page}]; !ok {
					return fmt.Errorf("valid page %s has no LBA", PPA{Block: block, Page: page})
				} else if d.mapping[lba] != (PPA{Block: block, Page: page}) {
					return fmt.Errorf("valid page %s mapping mismatch", PPA{Block: block, Page: page})
				}
			}
		}
	}
	if validCount != len(d.mapping) {
		return fmt.Errorf("valid page count %d != mapping count %d", validCount, len(d.mapping))
	}
	return nil
}

func (d *Device) pageStateLocked(ppa PPA) PageState {
	if !d.flash.Programmed(ppa.Block, ppa.Page) {
		data, oob, _, err := d.flash.ReadRaw(ppa.Block, ppa.Page)
		if err != nil || (allZero(data) && allZero(oob)) {
			return PageFree
		}
	}
	if d.valid[ppa.Block][ppa.Page] {
		return PageValid
	}
	meta, ok := d.flash.PageMeta(ppa.Block, ppa.Page)
	if !ok || meta.Seq == 0 {
		return PageTorn
	}
	return PageInvalid
}
