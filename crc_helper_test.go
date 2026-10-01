package bitpack

import (
	"encoding/binary"
	"hash/crc32"
)

func putCRC32(dst []byte, payload []byte) {
	binary.LittleEndian.PutUint32(dst, crc32.ChecksumIEEE(payload))
}
