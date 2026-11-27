package ftl

import "errors"

var (
	// ErrInvalidConfig indicates an impossible flash geometry or policy.
	ErrInvalidConfig = errors.New("ftl: invalid configuration")
	// ErrOutOfRange indicates a block, page, LBA, or range outside the device.
	ErrOutOfRange = errors.New("ftl: address out of range")
	// ErrPageTooLarge indicates a payload larger than one physical page.
	ErrPageTooLarge = errors.New("ftl: payload larger than page size")
	// ErrNoSpace indicates that every data block is full of live data.
	ErrNoSpace = errors.New("ftl: no reclaimable space")
	// ErrNotErased is returned when programming a block that is not erased.
	ErrNotErased = errors.New("ftl: block is not erased")
	// ErrNotSequential is returned when a page is programmed out of order.
	ErrNotSequential = errors.New("ftl: page program is not sequential")
	// ErrBlockFull is returned when the sequential write pointer reached P.
	ErrBlockFull = errors.New("ftl: block is full")
	// ErrPoweredOff is returned by an FTL whose controller lost power.
	ErrPoweredOff = errors.New("ftl: device is powered off; call Recover")
	// ErrProgramTorn indicates an injected page-program tear.  The page was
	// consumed by the flash but its checksum is invalid, so recovery discards
	// it and the logical operation is not acknowledged.
	ErrProgramTorn = errors.New("ftl: page program torn")
	// ErrCorruptMetadata is returned when persisted metadata fails its CRC.
	ErrCorruptMetadata = errors.New("ftl: corrupt metadata")
)
