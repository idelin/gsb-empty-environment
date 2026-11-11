package replay

import "hash/crc32"

// crcTable is the IEEE CRC-32 table used to protect log frames against torn or
// corrupted writes.
var crcTable = crc32.MakeTable(crc32.IEEE)

func crc32Checksum(payload []byte) uint32 {
	return crc32.Checksum(payload, crcTable)
}
