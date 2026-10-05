package main

import (
	"encoding/binary"
	"hash/crc32"
)

// 数据记录布局（data 文件）：
//
//	+0  magic   uint32  固定 recordMagic，用于区分“可 framing 的记录”与垃圾
//	+4  length  uint32  payload 长度
//	+8  crc     uint32  payload 的 CRC32(Castagnoli)
//	+12 payload [length]byte
//
// 索引项布局（index 文件，定长 indexEntrySize=16）：
//
//	+0  offset  uint64  记录在 data 文件中的偏移
//	+8  length  uint32  payload 长度（记录总长 = recordHeaderSize+length）
//	+12 digest  uint32  payload 的 CRC32，与数据头中的 crc 互相绑定
const (
	recordMagic      uint32 = 0x4C474352 // "LGCR"
	recordHeaderSize        = 12
	indexEntrySize          = 16
)

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

func checksum(payload []byte) uint32 {
	return crc32.Checksum(payload, castagnoli)
}

// encodeRecord 序列化一条数据记录。
func encodeRecord(payload []byte) []byte {
	buf := make([]byte, recordHeaderSize+len(payload))
	binary.LittleEndian.PutUint32(buf[0:4], recordMagic)
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(payload)))
	binary.LittleEndian.PutUint32(buf[8:12], checksum(payload))
	copy(buf[recordHeaderSize:], payload)
	return buf
}

// indexEntry 绑定一条记录的偏移、长度与摘要。
type indexEntry struct {
	Offset uint64
	Length uint32
	Digest uint32
}

func encodeIndexEntry(e indexEntry) []byte {
	buf := make([]byte, indexEntrySize)
	binary.LittleEndian.PutUint64(buf[0:8], e.Offset)
	binary.LittleEndian.PutUint32(buf[8:12], e.Length)
	binary.LittleEndian.PutUint32(buf[12:16], e.Digest)
	return buf
}

func decodeIndexEntry(buf []byte) indexEntry {
	return indexEntry{
		Offset: binary.LittleEndian.Uint64(buf[0:8]),
		Length: binary.LittleEndian.Uint32(buf[8:12]),
		Digest: binary.LittleEndian.Uint32(buf[12:16]),
	}
}
