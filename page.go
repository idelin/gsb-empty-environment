package pagedb

import (
	"encoding/binary"
	"errors"
	"hash"
	"hash/crc32"
)

// PageSize 是页文件中的固定页框大小。
const PageSize = 4096

// 页布局（小端，共 PageSize 字节）：
//
//	[0:4]   magic "PG01"
//	[4:12]  版本号（= 写入本页的 WAL 记录 LSN）
//	[12:16] 数据长度
//	[16:20] CRC32，覆盖 [4:16] 与 [20:]（即除 magic 与自身外全部字节）
//	[20:]   数据，剩余字节补零
const pageHeaderSize = 20

// MaxPageData 是单页可承载的最大用户数据量。
const MaxPageData = PageSize - pageHeaderSize

const pageMagic uint32 = 0x31304750 // "PG01"

// ErrPageCorrupt 表示页校验和或 magic 校验失败。
var ErrPageCorrupt = errors.New("pagedb: page checksum mismatch")

// ErrPageDataTooLarge 表示单次写入超过单页容量。
var ErrPageDataTooLarge = errors.New("pagedb: page data exceeds MaxPageData")

// pageChecksum 覆盖 [4:16]（版本+长度）与 [20:]（数据区），跳过 CRC 自身。
func pageChecksum(buf []byte) uint32 {
	var h hash.Hash32 = crc32.NewIEEE()
	h.Write(buf[4:16])
	h.Write(buf[20:])
	return h.Sum32()
}

func encodePage(version uint64, data []byte) []byte {
	buf := make([]byte, PageSize)
	binary.LittleEndian.PutUint32(buf[0:4], pageMagic)
	binary.LittleEndian.PutUint64(buf[4:12], version)
	binary.LittleEndian.PutUint32(buf[12:16], uint32(len(data)))
	copy(buf[pageHeaderSize:], data)
	binary.LittleEndian.PutUint32(buf[16:20], pageChecksum(buf))
	return buf
}

func decodePage(buf []byte) (version uint64, data []byte, err error) {
	if len(buf) != PageSize {
		return 0, nil, ErrPageCorrupt
	}
	if binary.LittleEndian.Uint32(buf[0:4]) != pageMagic {
		return 0, nil, ErrPageCorrupt
	}
	if binary.LittleEndian.Uint32(buf[16:20]) != pageChecksum(buf) {
		return 0, nil, ErrPageCorrupt
	}
	version = binary.LittleEndian.Uint64(buf[4:12])
	n := binary.LittleEndian.Uint32(buf[12:16])
	if n > MaxPageData {
		return 0, nil, ErrPageCorrupt
	}
	return version, append([]byte(nil), buf[pageHeaderSize:pageHeaderSize+n]...), nil
}
