//go:build !linux && !darwin

package netsec

func platformGetXAttr(path, name string, dest []byte) (int, error) {
	return 0, ErrXAttrUnsupported
}

func platformSetXAttr(path, name string, data []byte, flags int) error {
	return ErrXAttrUnsupported
}

func platformRemoveXAttr(path, name string) error {
	return ErrXAttrUnsupported
}

func isNoAttr(error) bool { return false }
