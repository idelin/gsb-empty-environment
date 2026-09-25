package deque

import (
	"encoding/binary"
	"hash/crc32"
)

// 元数据布局(小端,共 40 字节,末尾 CRC32 校验):
//   magic(4) version(4) elemSize(4) elemsPerBlock(4) maxBlocks(4)
//   head(8) count(8) crc32(4)
//
// head 是逻辑槽位号(单调),第一个元素所在槽位;
// tail = head + count。count == 0 时 head == tail,队列空。
const (
	metaMagic   = 0x44455131 // "DEQ1"
	metaVersion = 1
	metaByteLen = 40
)

// initialHead 给逻辑槽位号一个足够大的初值,
// 使得左推导致的 head 自减在实际使用范围内不会下溢。
const initialHead = uint64(1) << 62

type meta struct {
	elemSize      uint32 // 每个元素定长字节数
	elemsPerBlock uint32 // 每块容纳的元素个数
	maxBlocks     uint32 // 块文件编号环形空间大小
	head          uint64 // 逻辑槽位:第一个元素
	count         uint64 // 当前元素个数
}

func (m meta) tail() uint64 { return m.head + m.count }

func (m meta) marshal() []byte {
	buf := make([]byte, metaByteLen)
	binary.LittleEndian.PutUint32(buf[0:], metaMagic)
	binary.LittleEndian.PutUint32(buf[4:], metaVersion)
	binary.LittleEndian.PutUint32(buf[8:], m.elemSize)
	binary.LittleEndian.PutUint32(buf[12:], m.elemsPerBlock)
	binary.LittleEndian.PutUint32(buf[16:], m.maxBlocks)
	binary.LittleEndian.PutUint64(buf[20:], m.head)
	binary.LittleEndian.PutUint64(buf[28:], m.count)
	binary.LittleEndian.PutUint32(buf[36:], crc32.ChecksumIEEE(buf[:36]))
	return buf
}

func parseMeta(buf []byte) (meta, error) {
	var m meta
	if len(buf) != metaByteLen {
		return m, ErrCorrupt
	}
	if binary.LittleEndian.Uint32(buf[0:]) != metaMagic ||
		binary.LittleEndian.Uint32(buf[4:]) != metaVersion {
		return m, ErrCorrupt
	}
	if crc32.ChecksumIEEE(buf[:36]) != binary.LittleEndian.Uint32(buf[36:]) {
		return m, ErrCorrupt
	}
	m.elemSize = binary.LittleEndian.Uint32(buf[8:])
	m.elemsPerBlock = binary.LittleEndian.Uint32(buf[12:])
	m.maxBlocks = binary.LittleEndian.Uint32(buf[16:])
	m.head = binary.LittleEndian.Uint64(buf[20:])
	m.count = binary.LittleEndian.Uint64(buf[28:])
	if m.elemSize == 0 || m.elemsPerBlock == 0 || m.maxBlocks < 2 {
		return m, ErrCorrupt
	}
	return m, nil
}
