package cidx

import "errors"

var (
	ErrEmptyFile          = errors.New("cidx: empty file is not a valid index")
	ErrTooShort           = errors.New("cidx: file shorter than header")
	ErrBadMagic           = errors.New("cidx: bad magic")
	ErrUnsupportedVersion = errors.New("cidx: unsupported format version")
	ErrOverflow           = errors.New("cidx: integer overflow in layout arithmetic")
	ErrRegionOverlap      = errors.New("cidx: layout regions overlap")
	ErrOffsetCycle        = errors.New("cidx: offset table contains a back-edge (cycle)")
	ErrOffsetOutOfRange   = errors.New("cidx: offset points outside data region")
	ErrTruncated          = errors.New("cidx: file truncated relative to declared layout")
	ErrTrailingData       = errors.New("cidx: trailing bytes after declared layout")
	ErrRecordMalformed    = errors.New("cidx: record does not parse within its extent")
	ErrKeyOrder           = errors.New("cidx: keys not strictly increasing")
	ErrUnvalidatedRead    = errors.New("cidx: read of unvalidated region refused")
	ErrOutOfBounds        = errors.New("cidx: read out of bounds")
	ErrViewClosed         = errors.New("cidx: view closed")
	ErrCursorClosed       = errors.New("cidx: cursor closed")
	ErrMappingClosing     = errors.New("cidx: mapping is closing")
	ErrNoMapping          = errors.New("cidx: store has no mapping")
	ErrNotFound           = errors.New("cidx: key not found")
	ErrBuilderKeyOrder    = errors.New("cidx: builder keys must be added in strictly increasing order")
	ErrTooLarge           = errors.New("cidx: data region exceeds 32-bit offset range")
)
