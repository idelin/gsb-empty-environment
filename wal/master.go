package wal

import (
	"encoding/binary"
	"fmt"
	"os"
)

// WriteMaster persists the LSN of the most recent checkpoint.
func WriteMaster(path string, lsn uint64) error {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], lsn)
	return os.WriteFile(path, buf[:], 0o644)
}

// ReadMaster returns the LSN of the most recent checkpoint, or 0 if
// no checkpoint was ever taken.
func ReadMaster(path string) (uint64, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if len(b) != 8 {
		return 0, fmt.Errorf("wal: corrupt master record (len=%d)", len(b))
	}
	return binary.BigEndian.Uint64(b), nil
}
