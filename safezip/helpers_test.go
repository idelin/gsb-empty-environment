package safezip

import (
	"encoding/binary"
	"errors"
	"os"
)

func errorIs(err, target error) bool { return errors.Is(err, target) }

func testConfig() Config {
	return Config{ArchiveID: "obj-1", Limits: DefaultLimits()}
}

func u32(v uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	return b
}

func u32At(b []byte, off int64) uint32 {
	return binary.LittleEndian.Uint32(b[off:])
}

func binaryPutU32(b []byte, v uint32) {
	binary.LittleEndian.PutUint32(b, v)
}

func binaryPutU16(b []byte, v uint16) {
	binary.LittleEndian.PutUint16(b, v)
}

func readDirNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names, nil
}
