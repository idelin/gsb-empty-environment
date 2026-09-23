package blkconv

import (
	"encoding/binary"
	"hash/crc32"
	"hash/crc64"
)

func crc32sum(b []byte) []byte {
	var s [4]byte
	binary.BigEndian.PutUint32(s[:], crc32.ChecksumIEEE(b))
	return s[:]
}

func crc64sum(b []byte) []byte {
	var s [8]byte
	binary.BigEndian.PutUint64(s[:], crc64.Checksum(b, crc64Table))
	return s[:]
}
