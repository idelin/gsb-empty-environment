//go:build darwin && !cgo

package netsec

func platformReadXattr(path, name string) ([]byte, error) {
	return nil, ErrXattrUnsupported
}

func platformSetXattr(path, name string, data []byte) error {
	return ErrXattrUnsupported
}

func platformRemoveXattr(path, name string) error {
	return ErrXattrUnsupported
}
