package ftl

import (
	"fmt"
	"sync"
)

// Flash is the persistent, power-fail-safe model of a NAND chip.  It owns only
// physical pages and erase counters; the FTL mapping lives above it.
//
// Hardware rules enforced here:
//   - a block must be erased before any of its pages can be programmed;
//   - pages within a block are programmed strictly in order 0..P-1;
//   - a page can be programmed at most once between erases;
//   - erase is block granular and increments the block erase counter.
//
// A "torn" program writes only a prefix of the data/metadata and consumes the
// page.  Its OOB checksum is intentionally invalid so recovery can discard it
// while still advancing the block write pointer.
type Flash struct {
	mu sync.RWMutex

	blockCount    int
	pagesPerBlock int
	pageSize      int
	oobSize       int

	data  [][][]byte
	oob   [][][]byte
	used  [][]bool
	next  []int
	erase []uint64

	programs uint64
	erases   uint64
	torn     uint64
}

// NewFlash constructs an empty, fully erased flash chip.
func NewFlash(cfg Config) (*Flash, error) {
	cfg, err := cfg.normalize()
	if err != nil {
		return nil, err
	}
	return newFlashGeometry(cfg.BlockCount, cfg.PagesPerBlock, cfg.PageSize, cfg.OOBSize), nil
}

func newFlashGeometry(blocks, pages, pageSize, oobSize int) *Flash {
	f := &Flash{
		blockCount:    blocks,
		pagesPerBlock: pages,
		pageSize:      pageSize,
		oobSize:       oobSize,
		data:          make([][][]byte, blocks),
		oob:           make([][][]byte, blocks),
		used:          make([][]bool, blocks),
		next:          make([]int, blocks),
		erase:         make([]uint64, blocks),
	}
	for block := 0; block < blocks; block++ {
		f.data[block] = make([][]byte, pages)
		f.oob[block] = make([][]byte, pages)
		f.used[block] = make([]bool, pages)
	}
	return f
}

// BlockCount returns the number of physical data blocks.
func (f *Flash) BlockCount() int { return f.blockCount }

// PagesPerBlock returns the number of pages in each physical block.
func (f *Flash) PagesPerBlock() int { return f.pagesPerBlock }

// PageSize returns the user-data capacity of one page.
func (f *Flash) PageSize() int { return f.pageSize }

// OOBSize returns the Out-of-Band capacity of one page.
func (f *Flash) OOBSize() int { return f.oobSize }

// Program writes one page.  data is zero-padded to PageSize and oob must not
// exceed OOBSize.  Programming an out-of-sequence page, a non-erased block, or
// an already-programmed page fails without consuming the page.
func (f *Flash) Program(block, page int, data, oob []byte) error {
	return f.program(block, page, data, oob, len(data), len(oob), false)
}

// ProgramPartial performs a deliberately torn page program.  Only dataBytes of
// data and oobBytes of oob reach the medium.  This is useful for fault
// injection; normal FTL code uses Program.
func (f *Flash) ProgramPartial(block, page int, data, oob []byte, dataBytes, oobBytes int) error {
	return f.program(block, page, data, oob, dataBytes, oobBytes, true)
}

// ProgramTorn is an alias for ProgramPartial.
func (f *Flash) ProgramTorn(block, page int, data, oob []byte, dataBytes, oobBytes int) error {
	return f.ProgramPartial(block, page, data, oob, dataBytes, oobBytes)
}

func (f *Flash) program(block, page int, data, oob []byte, dataBytes, oobBytes int, torn bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if block < 0 || block >= f.blockCount || page < 0 || page >= f.pagesPerBlock {
		return fmt.Errorf("%w: block=%d page=%d", ErrOutOfRange, block, page)
	}
	if len(data) > f.pageSize {
		return fmt.Errorf("%w: %d > %d", ErrPageTooLarge, len(data), f.pageSize)
	}
	if len(oob) > f.oobSize {
		return fmt.Errorf("%w: oob %d > %d", ErrPageTooLarge, len(oob), f.oobSize)
	}
	if f.next[block] >= f.pagesPerBlock {
		return fmt.Errorf("%w: block=%d", ErrBlockFull, block)
	}
	if page != f.next[block] {
		return fmt.Errorf("%w: block=%d next=%d got=%d", ErrNotSequential, block, f.next[block], page)
	}
	if f.used[block][page] {
		return fmt.Errorf("%w: block=%d page=%d already programmed", ErrNotErased, block, page)
	}

	if dataBytes < 0 {
		dataBytes = 0
	}
	if dataBytes > len(data) {
		dataBytes = len(data)
	}
	if oobBytes < 0 {
		oobBytes = 0
	}
	if oobBytes > len(oob) {
		oobBytes = len(oob)
	}

	pageData := make([]byte, f.pageSize)
	copy(pageData, data[:dataBytes])
	// A real torn page may contain a mix of old and new bits.  Deterministic
	// non-zero poisoning makes the tear observable even when the caller wrote
	// a zero payload, while the intentionally broken metadata checksum is
	// what recovery actually keys on.
	if torn && dataBytes < len(data) {
		for i := dataBytes; i < len(data) && i < f.pageSize; i++ {
			pageData[i] = 0xA5
		}
	}
	pageOOB := make([]byte, f.oobSize)
	copy(pageOOB, oob[:oobBytes])
	if torn && oobBytes < len(oob) {
		for i := oobBytes; i < len(oob) && i < f.oobSize; i++ {
			pageOOB[i] = 0x5A
		}
	}

	f.data[block][page] = pageData
	f.oob[block][page] = pageOOB
	f.used[block][page] = true
	f.next[block] = page + 1
	f.programs++
	if torn {
		f.torn++
	}
	return nil
}

// ProgramCorrupt writes a page whose OOB bytes are stored as supplied.  It is
// a low-level fault-injection helper; normal programming goes through Program.
func (f *Flash) ProgramCorrupt(block, page int, data, oob []byte) error {
	return f.program(block, page, data, oob, len(data), len(oob), true)
}

// ReadRaw returns copies of a page's data and OOB plus whether the medium ever
// programmed that slot.
func (f *Flash) ReadRaw(block, page int) (data, oob []byte, programmed bool, err error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if block < 0 || block >= f.blockCount || page < 0 || page >= f.pagesPerBlock {
		return nil, nil, false, fmt.Errorf("%w: block=%d page=%d", ErrOutOfRange, block, page)
	}
	programmed = f.used[block][page]
	if !programmed {
		return make([]byte, f.pageSize), make([]byte, f.oobSize), false, nil
	}
	data = append([]byte(nil), f.data[block][page]...)
	oob = append([]byte(nil), f.oob[block][page]...)
	return data, oob, true, nil
}

// ReadPage is a convenience wrapper around ReadRaw.
func (f *Flash) ReadPage(block, page int) ([]byte, []byte, error) {
	data, oob, _, err := f.ReadRaw(block, page)
	return data, oob, err
}

// Erase erases one whole block and increments its erase counter.
func (f *Flash) Erase(block int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if block < 0 || block >= f.blockCount {
		return fmt.Errorf("%w: block=%d", ErrOutOfRange, block)
	}
	for page := 0; page < f.pagesPerBlock; page++ {
		f.data[block][page] = nil
		f.oob[block][page] = nil
		f.used[block][page] = false
	}
	f.next[block] = 0
	f.erase[block]++
	f.erases++
	return nil
}

// EraseBlock is an alias for Erase.
func (f *Flash) EraseBlock(block int) error { return f.Erase(block) }

// EraseCount returns the number of erases performed on a block.
func (f *Flash) EraseCount(block int) uint64 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if block < 0 || block >= f.blockCount {
		return 0
	}
	return f.erase[block]
}

// WritePointer returns the next sequential page index of a block.
func (f *Flash) WritePointer(block int) int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if block < 0 || block >= f.blockCount {
		return 0
	}
	return f.next[block]
}

// IsErased reports whether a block has no programmed pages.
func (f *Flash) IsErased(block int) bool { return f.WritePointer(block) == 0 }

// Programmed reports whether a physical slot has ever been programmed since
// the last erase.
func (f *Flash) Programmed(block, page int) bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if block < 0 || block >= f.blockCount || page < 0 || page >= f.pagesPerBlock {
		return false
	}
	return f.used[block][page]
}

// InjectOOBFlip flips one bit in the stored OOB bytes of a programmed page.
// Recovery must treat the page as torn/unreadable.
func (f *Flash) InjectOOBFlip(block, page, offset int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if block < 0 || block >= f.blockCount || page < 0 || page >= f.pagesPerBlock {
		return fmt.Errorf("%w: block=%d page=%d", ErrOutOfRange, block, page)
	}
	if !f.used[block][page] {
		return fmt.Errorf("%w: page not programmed", ErrOutOfRange)
	}
	if offset < 0 || offset >= len(f.oob[block][page]) {
		return fmt.Errorf("%w: oob offset=%d", ErrOutOfRange, offset)
	}
	f.oob[block][page][offset] ^= 0x01
	return nil
}

// CorruptPage flips the stored CRC byte of a programmed page, making recovery
// treat it as a torn/half-written page.
func (f *Flash) CorruptPage(block, page int) error {
	return f.InjectOOBFlip(block, page, 36)
}

// InjectDataFlip flips one bit in a programmed page's user data.  Because the
// OOB checksum covers the data, recovery treats the page as torn.
func (f *Flash) InjectDataFlip(block, page, offset int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if block < 0 || block >= f.blockCount || page < 0 || page >= f.pagesPerBlock {
		return fmt.Errorf("%w: block=%d page=%d", ErrOutOfRange, block, page)
	}
	if !f.used[block][page] {
		return fmt.Errorf("%w: page not programmed", ErrOutOfRange)
	}
	if offset < 0 || offset >= len(f.data[block][page]) {
		return fmt.Errorf("%w: data offset=%d", ErrOutOfRange, offset)
	}
	f.data[block][page][offset] ^= 0x01
	return nil
}

// Stats summarises low-level wear and tear.
type FlashStats struct {
	Programs   uint64
	TornWrites uint64
	Erases     uint64
	Programmed int
}

// Stats returns a snapshot of the chip counters.
func (f *Flash) Stats() FlashStats {
	f.mu.RLock()
	defer f.mu.RUnlock()
	programmed := 0
	for block := range f.used {
		for _, used := range f.used[block] {
			if used {
				programmed++
			}
		}
	}
	return FlashStats{
		Programs:   f.programs,
		TornWrites: f.torn,
		Erases:     f.erases,
		Programmed: programmed,
	}
}

// Clone returns a deep copy of the flash chip.  It is useful for building
// deterministic crash-recovery test matrices.
func (f *Flash) Clone() *Flash {
	f.mu.RLock()
	defer f.mu.RUnlock()
	clone := newFlashGeometry(f.blockCount, f.pagesPerBlock, f.pageSize, f.oobSize)
	clone.programs = f.programs
	clone.erases = f.erases
	clone.torn = f.torn
	copy(clone.erase, f.erase)
	for block := 0; block < f.blockCount; block++ {
		clone.next[block] = f.next[block]
		for page := 0; page < f.pagesPerBlock; page++ {
			clone.used[block][page] = f.used[block][page]
			if f.data[block][page] != nil {
				clone.data[block][page] = append([]byte(nil), f.data[block][page]...)
			}
			if f.oob[block][page] != nil {
				clone.oob[block][page] = append([]byte(nil), f.oob[block][page]...)
			}
		}
	}
	return clone
}
