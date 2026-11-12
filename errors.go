package netsec

import "errors"

// Sentinel errors returned for malformed SSZ input. Use errors.Is to detect a
// specific failure class; wrapped errors carry contextual detail.
var (
	// ErrTruncated means the input ends before the declared type is complete.
	ErrTruncated = errors.New("ssz: truncated input")
	// ErrTrailingBytes means bytes remain after the declared value ends.
	ErrTrailingBytes = errors.New("ssz: trailing bytes after end of value")
	// ErrMaxSize means the encoding exceeds the type's maximum wire size.
	ErrMaxSize = errors.New("ssz: encoding exceeds maximum size")
	// ErrOffsetFirst means the first variable-field offset does not equal the
	// fixed-part length.
	ErrOffsetFirst = errors.New("ssz: first offset must equal fixed part size")
	// ErrOffsetOrder means two consecutive offsets decrease.
	ErrOffsetOrder = errors.New("ssz: offsets must be non-decreasing")
	// ErrOffsetRange means an offset points past the end of the input.
	ErrOffsetRange = errors.New("ssz: offset out of range")
	// ErrLengthExceeded means a list carries more elements than its bound.
	ErrLengthExceeded = errors.New("ssz: list length exceeds type limit")
	// ErrBitlistDelimiter means a bitlist lacks a legal terminal set bit.
	ErrBitlistDelimiter = errors.New("ssz: illegal bitlist delimiter bit")
	// ErrUnionSelector means the union selector names no member type.
	ErrUnionSelector = errors.New("ssz: union selector out of range")
	// ErrBoolValue means a boolean byte is neither 0 nor 1.
	ErrBoolValue = errors.New("ssz: boolean value must be 0 or 1")
	// ErrTypeMismatch means a Value accessor was used on the wrong kind.
	ErrTypeMismatch = errors.New("ssz: value is not of the requested kind")
)
