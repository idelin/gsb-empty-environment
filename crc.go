package rlecomp

import "hash/crc32"

func crc32Of(b []byte) uint32 { return crc32.ChecksumIEEE(b) }
