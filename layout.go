package netsec

import "encoding/binary"

// 共享页头部布局（小端序）。
//
//	偏移 0  uint32   magic    固定魔数，标识共享页格式
//	偏移 4  uint32   version  布局版本
//	偏移 8  uint64   gen      写入代次，序号回绕时递增
//	偏移 16 uint64   seq      序列号：偶数=稳定，奇数=写入进行中
//	偏移 24 uint32   capacity 记录区容量（字节）
//	偏移 28 uint32   length   当前记录长度（字节）
//	偏移 32 [32]byte digest   当前记录的 SHA-256 摘要
//	偏移 64 ...      记录区
const (
	headerSize = 64

	offMagic    = 0
	offVersion  = 4
	offGen      = 8
	offSeq      = 16
	offCapacity = 24
	offLength   = 28
	offDigest   = 32

	// magic 为 ASCII "SPSH" 的小端表示（Shared Page Seqlock Header）。
	magic   = uint32('S') | uint32('P')<<8 | uint32('S')<<16 | uint32('H')<<24
	version = uint32(1)

	digestSize = 32
)

var le = binary.LittleEndian

// PageSize 返回给定记录区容量对应的整页大小，按 64 字节向上取整。
func PageSize(capacity uint32) int {
	return headerSize + int((capacity+63)&^uint32(63))
}
