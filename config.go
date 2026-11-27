package ftl

import (
	"fmt"
	"math"
)

const (
	// DefaultBlockCount is used when Config leaves the geometry unspecified.
	DefaultBlockCount = 32
	// DefaultPagesPerBlock is used when Config leaves the geometry unspecified.
	DefaultPagesPerBlock = 64
	// DefaultPageSize is the default user-data bytes per NAND page.
	DefaultPageSize = 4096
	// DefaultOOBytes is the default Out-of-Band metadata area per page.
	DefaultOOBytes = 64
)

// Config describes the geometry and policy of an FTL instance.
//
// Only the total number of physical data blocks is configured.  Checkpoint and
// TRIM journal records live in a separate persistent metadata area, mirroring
// the dedicated metadata region of a real SSD controller; they therefore do
// not reduce the user-visible block pool.
//
// Several fields have aliases (for example Blocks and BlockCount).  This is
// intentional: the package is meant to be easy to embed into simulators with
// different naming conventions.  The canonical field wins when both are set.
type Config struct {
	// Canonical geometry.
	BlockCount    int
	PagesPerBlock int
	PageSize      int
	OOBSize       int

	// Aliases for geometry.
	Blocks       int
	NumBlocks    int
	Pages        int
	PageBytes    int
	OOBytes      int
	OOBSizeBytes int

	// Over-provisioning policy.  ReservedFreeBlocks is the number of erased
	// blocks that are kept in reserve.  GC starts before that reserve is
	// consumed, so a GC destination block always exists.
	ReservedFreeBlocks  int
	OverProvision       int
	OverProvisionBlocks int
	ReservedBlocks      int
	GCWatermark         int

	// WearLevelThreshold is the maximum erase-count spread that the wear
	// leveler tolerates.  When the hottest and coldest used blocks differ by
	// more than this, GC prefers to migrate a static cold block.  Defaults to
	// 2 when zero.
	WearLevelThreshold uint64
	WearThreshold      uint64
	MaxEraseDelta      uint64

	// MaxGCPerWrite bounds the number of GC victims reclaimed by a single
	// Write/CollectGarbage call.  Zero means no explicit bound (the device
	// still stops as soon as no productive victim exists).
	MaxGCPerWrite int

	// Seed is used for deterministic tie breaking in GC.
	Seed int64
}

// DefaultConfig returns a small, self-contained configuration suitable for
// tests and simulation.
func DefaultConfig() Config {
	return Config{
		BlockCount:         DefaultBlockCount,
		PagesPerBlock:      DefaultPagesPerBlock,
		PageSize:           DefaultPageSize,
		OOBSize:            DefaultOOBytes,
		ReservedFreeBlocks: 2,
		WearLevelThreshold: 2,
	}
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func firstNonZeroUint64(values ...uint64) uint64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

// normalize validates the configuration and fills in defaults.
func (c Config) normalize() (Config, error) {
	c.BlockCount = firstNonZero(c.BlockCount, c.Blocks, c.NumBlocks)
	c.PagesPerBlock = firstNonZero(c.PagesPerBlock, c.Pages)
	c.PageSize = firstNonZero(c.PageSize, c.PageBytes)
	c.OOBSize = firstNonZero(c.OOBSize, c.OOBytes, c.OOBSizeBytes)

	if c.BlockCount == 0 {
		c.BlockCount = DefaultBlockCount
	}
	if c.PagesPerBlock == 0 {
		c.PagesPerBlock = DefaultPagesPerBlock
	}
	if c.PageSize == 0 {
		c.PageSize = DefaultPageSize
	}
	if c.OOBSize == 0 {
		c.OOBSize = DefaultOOBytes
	}

	if c.BlockCount < 2 {
		return Config{}, fmt.Errorf("%w: BlockCount must be >= 2", ErrInvalidConfig)
	}
	if c.PagesPerBlock < 1 {
		return Config{}, fmt.Errorf("%w: PagesPerBlock must be >= 1", ErrInvalidConfig)
	}
	if c.PageSize < 1 {
		return Config{}, fmt.Errorf("%w: PageSize must be >= 1", ErrInvalidConfig)
	}
	if c.OOBSize < pageMetaSize {
		return Config{}, fmt.Errorf("%w: OOBSize must be >= %d", ErrInvalidConfig, pageMetaSize)
	}
	if c.PageSize > math.MaxInt32 {
		return Config{}, fmt.Errorf("%w: PageSize is too large", ErrInvalidConfig)
	}

	c.ReservedFreeBlocks = firstNonZero(c.ReservedFreeBlocks, c.OverProvision, c.OverProvisionBlocks, c.ReservedBlocks, c.GCWatermark)
	if c.ReservedFreeBlocks == 0 {
		c.ReservedFreeBlocks = 1
		if c.BlockCount >= 16 {
			c.ReservedFreeBlocks = c.BlockCount / 16
		}
	}
	if c.ReservedFreeBlocks < 1 {
		c.ReservedFreeBlocks = 1
	}
	// At least one block must remain allocatable for user data even in the
	// smallest configuration.
	if c.ReservedFreeBlocks >= c.BlockCount {
		c.ReservedFreeBlocks = c.BlockCount - 1
	}

	c.WearLevelThreshold = firstNonZeroUint64(c.WearLevelThreshold, c.WearThreshold, c.MaxEraseDelta)
	if c.WearLevelThreshold == 0 {
		c.WearLevelThreshold = 2
	}
	if c.MaxGCPerWrite < 0 {
		c.MaxGCPerWrite = 0
	}
	return c, nil
}
