package netsec

import (
	"errors"
	"fmt"
)

var (
	// ErrEmptyTable means that a table contains no target lines.
	ErrEmptyTable = errors.New("device mapper table is empty")
	// ErrMalformedTable means that a table line cannot be parsed.
	ErrMalformedTable = errors.New("malformed device mapper table")
	// ErrZeroLength means that a target has a zero-sector length.
	ErrZeroLength = errors.New("target has zero length")
	// ErrNotContiguous means that a table has a hole or overlapping targets.
	ErrNotContiguous = errors.New("table is not contiguous from sector 0")
	// ErrTotalOverflow means that the table's sector total overflows uint64.
	ErrTotalOverflow = errors.New("table sector total overflows uint64")
	// ErrUnsupportedTarget means that a target type is not allowed.
	ErrUnsupportedTarget = errors.New("unsupported target type")
	// ErrInvalidParameters means that a target's parameters are malformed.
	ErrInvalidParameters = errors.New("invalid target parameters")
	// ErrUnknownDevice means that a backing device is not registered.
	ErrUnknownDevice = errors.New("backing device is not registered")
	// ErrCycle means that a target references the mapping being created.
	ErrCycle = errors.New("target references the mapping itself")
	// ErrOutOfBounds means that a backing-device range exceeds its size.
	ErrOutOfBounds = errors.New("backing-device range is out of bounds")
	// ErrInsecureCipher means that crypt uses cipher_null or another null cipher.
	ErrInsecureCipher = errors.New("crypt target uses an insecure null cipher")
	// ErrUnsupportedCipher means that a crypt cipher is not in the key-size map.
	ErrUnsupportedCipher = errors.New("crypt cipher is not supported")
	// ErrInvalidKey means that a crypt key is not valid hexadecimal.
	ErrInvalidKey = errors.New("crypt key is not valid hexadecimal")
	// ErrKeyLength means that a crypt key length does not match its cipher.
	ErrKeyLength = errors.New("crypt key length does not match cipher")
	// ErrReadOnlyDevice means that a writable linear target exposes a read-only
	// backing device.
	ErrReadOnlyDevice = errors.New("read-only device exposed through writable linear target")
)

func segmentError(kind error, segment int, format string, args ...any) error {
	detail := fmt.Sprintf(format, args...)
	return fmt.Errorf("%w: segment %d: %s", kind, segment+1, detail)
}
