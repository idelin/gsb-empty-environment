package netsec

import (
	"errors"
	"fmt"
	"math"
)

const (
	// OffsetEntrySize is the size of one entry in a transaction's offset array.
	OffsetEntrySize = 8

	// FlatBinderObjectSize is the size of a flat_binder_object on a 64-bit
	// Binder ABI.
	FlatBinderObjectSize = 24
)

var (
	ErrInvalidSize          = errors.New("netsec: invalid transaction size")
	ErrSizeOverflow         = errors.New("netsec: transaction size overflow")
	ErrOffsetAlignment      = errors.New("netsec: offset is not 4-byte aligned")
	ErrOffsetOrder          = errors.New("netsec: offsets are not strictly increasing")
	ErrObjectBounds         = errors.New("netsec: flat_binder_object exceeds transaction data")
	ErrObjectOverlap        = errors.New("netsec: flat_binder_object overlaps another object")
	ErrCallerQuota          = errors.New("netsec: caller transaction quota exceeded")
	ErrOneWayQuota          = errors.New("netsec: one-way transaction buffer quota exceeded")
	ErrDuplicateTransaction = errors.New("netsec: transaction is already pending")
)

// TransactionSize returns the buffer occupancy of a transaction: its data
// length plus eight bytes for every offset entry, rounded up to an eight-byte
// boundary.
func TransactionSize(dataLength, offsetCount int) (int, error) {
	if dataLength < 0 || offsetCount < 0 {
		return 0, ErrInvalidSize
	}

	raw := int64(dataLength)
	if int64(offsetCount) > (math.MaxInt64-raw)/OffsetEntrySize {
		return 0, ErrSizeOverflow
	}
	raw += int64(offsetCount) * OffsetEntrySize
	if raw > math.MaxInt64-7 {
		return 0, ErrSizeOverflow
	}

	size := (raw + 7) &^ 7
	if size > int64(int(^uint(0)>>1)) {
		return 0, ErrSizeOverflow
	}
	return int(size), nil
}

// Validate checks the offset array of a transaction whose data occupies data.
// Every offset must be four-byte aligned and strictly increasing, and every
// 24-byte flat_binder_object starting at an offset must fit wholly inside data
// without overlapping another object.
func Validate(data []byte, offsets []uint64) error {
	return validateOffsets(data, offsets)
}

// ValidateTransaction is an alias for Validate.
func ValidateTransaction(data []byte, offsets []uint64) error {
	return Validate(data, offsets)
}

type unsignedOffset interface {
	~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr
}

// ValidateOffsets is the generic form of Validate for unsigned offset types.
func ValidateOffsets[T unsignedOffset](data []byte, offsets []T) error {
	return validateOffsets(data, offsets)
}

type signedOffset interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64
}

// ValidateSignedOffsets validates signed offsets. Negative offsets are
// rejected as out of bounds.
func ValidateSignedOffsets[T signedOffset](data []byte, offsets []T) error {
	var previous uint64
	var maxEnd uint64

	for i, raw := range offsets {
		if raw < 0 {
			return fmt.Errorf("%w: offset %d is negative", ErrObjectBounds, raw)
		}
		offset := uint64(raw)
		if err := validateObject(data, offset, i > 0, &previous, &maxEnd); err != nil {
			return err
		}
	}
	return nil
}

func validateOffsets[T unsignedOffset](data []byte, offsets []T) error {
	var previous uint64
	var maxEnd uint64

	for i, raw := range offsets {
		if err := validateObject(data, uint64(raw), i > 0, &previous, &maxEnd); err != nil {
			return err
		}
	}
	return nil
}

func validateObject(data []byte, offset uint64, hasPrevious bool, previous, maxEnd *uint64) error {
	if offset%4 != 0 {
		return fmt.Errorf("%w: offset %d", ErrOffsetAlignment, offset)
	}
	if hasPrevious && offset <= *previous {
		return fmt.Errorf("%w: offset %d follows %d", ErrOffsetOrder, offset, *previous)
	}

	dataLength := uint64(len(data))
	if offset > dataLength || FlatBinderObjectSize > dataLength-offset {
		return fmt.Errorf("%w: object at %d needs %d bytes, data has %d bytes",
			ErrObjectBounds, offset, FlatBinderObjectSize, dataLength)
	}

	end := offset + FlatBinderObjectSize
	if offset < *maxEnd {
		return fmt.Errorf("%w: object [%d,%d) overlaps [0,%d)",
			ErrObjectOverlap, offset, end, *maxEnd)
	}

	*previous = offset
	*maxEnd = end
	return nil
}
