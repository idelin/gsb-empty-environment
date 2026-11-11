package pagetable

import (
	"errors"
	"io"
)

// Page geometry for the x86-64 style four-level page table.
const (
	PageSize        = 4096
	LargePageSize   = 2 * 1024 * 1024
	entriesPerTab   = 512
	pageTableLevels = 4
)

// Table levels, root first.
const (
	LevelPML4 = 0
	LevelPDPT = 1
	LevelPD   = 2
	LevelPT   = 3
)

// Entry flag bits.
const (
	flagPresent  uint64 = 1 << 0
	flagRW       uint64 = 1 << 1
	flagUS       uint64 = 1 << 2
	flagPWT      uint64 = 1 << 3
	flagPCD      uint64 = 1 << 4
	flagAccessed uint64 = 1 << 5
	flagDirty    uint64 = 1 << 6
	flagPS       uint64 = 1 << 7 // page size (table levels) / PAT select (PT level)
	flagGlobal   uint64 = 1 << 8
	flagLargePAT uint64 = 1 << 12
	flagNX       uint64 = 1 << 63
)

// Physical frame masks.
const (
	tableFrameMask uint64 = 0x000f_ffff_ffff_f000
	largeFrameMask uint64 = 0x000f_ffff_ffe0_0000
	gibFrameMask   uint64 = 0x000f_ffff_fc00_0000
	leafFrameMask         = tableFrameMask
)

// Permission bits managed by transactions. Every other entry bit is preserved
// byte-for-byte on copied table pages.
var managedFlagMask = flagRW | flagUS | flagNX

var (
	// ErrNotCanonical is returned for a virtual address whose top 16 bits are
	// not a sign extension of bit 47.
	ErrNotCanonical = errors.New("virtual address is not canonical")
	// ErrNotMapped is returned when a walk reaches a non-present entry.
	ErrNotMapped = errors.New("virtual address is not mapped")
	// ErrInvalidRange is returned when a Patch range is empty or inverted.
	ErrInvalidRange = errors.New("invalid virtual address range")
	// ErrInvalidAlignment is returned when a range is not 4 KiB aligned.
	ErrInvalidAlignment = errors.New("virtual address is not 4 KiB aligned")
	// ErrUnsupportedPageSize is returned when an edit touches a 1 GiB page.
	ErrUnsupportedPageSize = errors.New("editing 1 GiB pages is not supported")
)

// PageStore gives random access to the physical address space backing a page
// table tree. Existing pages are treated as immutable: transactions only ever
// write freshly allocated pages, so old snapshots stay readable concurrently.
type PageStore interface {
	io.ReaderAt
	io.WriterAt
}

// Allocator hands out fresh, zeroed physical page frames. It is allowed to
// fail at any allocation (for example when exhausted); a failed allocation
// forces a full transaction rollback. FreePage returns frames owned by an
// aborted or collapsed transaction.
type Allocator interface {
	AllocPage() (addr uint64, err error)
	FreePage(addr uint64)
}

// Perm describes the effective permissions of a mapping.
type Perm struct {
	Write   bool
	User    bool
	Execute bool
}

// Patch requests effective Perm on every 4 KiB page in [Start, End).
// Both bounds must be canonical and 4 KiB aligned.
type Patch struct {
	Start uint64
	End   uint64
	Perm  Perm
}

func (p Patch) validate() error {
	if !Canonical(p.Start) || !Canonical(p.End) {
		return ErrNotCanonical
	}
	if p.Start&(PageSize-1) != 0 || p.End&(PageSize-1) != 0 {
		return ErrInvalidAlignment
	}
	if p.Start >= p.End {
		return ErrInvalidRange
	}
	return nil
}

// Canonical reports whether va is a canonical 48-bit virtual address:
// bits 48..63 must all equal bit 47.
func Canonical(va uint64) bool {
	// Bits 48..63 must all equal bit 47.
	top := va >> 47
	return top == 0 || top == (1<<17)-1
}

// entryIndex returns the 9-bit index used for the given table level.
// Level 0 (PML4) uses bits 39..47, level 3 (PT) bits 12..20.
func entryIndex(va uint64, level int) uint {
	shift := uint(12 + 9*(pageTableLevels-1-level))
	return uint((va >> shift) & 0x1ff)
}

// entrySpan returns the virtual span covered by one entry at this level.
func entrySpan(level int) uint64 {
	shift := uint(12 + 9*(pageTableLevels-1-level))
	return uint64(1) << shift
}

func entryPresent(entry uint64) bool { return entry&flagPresent != 0 }

func permFromEntry(entry uint64) Perm {
	return Perm{
		Write:   entry&flagRW != 0,
		User:    entry&flagUS != 0,
		Execute: entry&flagNX == 0,
	}
}

// combinePerm intersects permissions accumulated along a walk.
func combinePerm(acc Perm, entry uint64) Perm {
	p := permFromEntry(entry)
	return Perm{
		Write:   acc.Write && p.Write,
		User:    acc.User && p.User,
		Execute: acc.Execute && p.Execute,
	}
}

// setLeafPerm rewrites the managed permission bits of a leaf entry so that it
// carries exactly p (revocations live at the leaf, grants are relaxed upward).
func setLeafPerm(entry uint64, p Perm) uint64 {
	entry &^= managedFlagMask
	if p.Write {
		entry |= flagRW
	}
	if p.User {
		entry |= flagUS
	}
	if !p.Execute {
		entry |= flagNX
	}
	return entry
}

// relaxForChild grants permissions on an ancestor entry. Bits are only ever
// added (or NX cleared), never revoked, so sibling mappings keep their
// effective permissions via the leaf-level AND.
func relaxForChild(entry uint64, p Perm) uint64 {
	if p.Write {
		entry |= flagRW
	}
	if p.User {
		entry |= flagUS
	}
	if p.Execute {
		entry &^= flagNX
	}
	return entry
}
