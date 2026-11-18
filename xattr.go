package netsec

import (
	"errors"
	"fmt"
	"syscall"
)

// ErrXAttrUnsupported is returned on platforms without extended attribute
// support.
var ErrXAttrUnsupported = errors.New("netsec: extended attributes are not supported on this platform")

const maxXAttrSize = 1 << 20

// readXAttr reads name from path. Attribute sizes may change between the size
// query and the read, so ERANGE is handled by retrying with a larger buffer.
func readXAttr(path, name string) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: empty path", ErrMalformedAttribute)
	}
	if name == "" {
		return nil, fmt.Errorf("%w: empty attribute name", ErrMalformedAttribute)
	}

	size, err := platformGetXAttr(path, name, nil)
	if err != nil {
		if isNoAttr(err) {
			return nil, ErrNoAttribute
		}
		return nil, err
	}
	if size == 0 {
		return []byte{}, nil
	}
	if size < 0 || size > maxXAttrSize {
		return nil, fmt.Errorf("%w: invalid attribute size %d", ErrMalformedAttribute, size)
	}

	for {
		buf := make([]byte, size)
		n, err := platformGetXAttr(path, name, buf)
		if err == nil {
			if n < 0 || n > len(buf) {
				return nil, fmt.Errorf("%w: attribute read returned %d bytes into a %d-byte buffer",
					ErrMalformedAttribute, n, len(buf))
			}
			return buf[:n], nil
		}
		if !errors.Is(err, syscall.ERANGE) {
			if isNoAttr(err) {
				return nil, ErrNoAttribute
			}
			return nil, err
		}
		if size >= maxXAttrSize {
			return nil, fmt.Errorf("%w: attribute exceeds %d bytes", ErrMalformedAttribute, maxXAttrSize)
		}
		size *= 2
		if size > maxXAttrSize {
			size = maxXAttrSize
		}
	}
}

// SetXAttr writes an extended attribute. It is provided for provisioning and
// tests; evaluation itself never writes security metadata.
func SetXAttr(path, name string, data []byte) error {
	return platformSetXAttr(path, name, data, 0)
}

// RemoveXAttr removes an extended attribute.
func RemoveXAttr(path, name string) error {
	return platformRemoveXAttr(path, name)
}
