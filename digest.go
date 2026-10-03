package main

import (
	"encoding/binary"
	"hash/crc32"
)

// NumBuckets 将哈希环切分为固定数量的区间，用于失配时的区间对账。
const NumBuckets = 16

// MemberState 成员状态。
type MemberState uint8

const (
	Alive MemberState = iota
	Suspect
	Dead
)

func (s MemberState) String() string {
	switch s {
	case Alive:
		return "Alive"
	case Suspect:
		return "Suspect"
	case Dead:
		return "Dead"
	}
	return "Unknown"
}

// Member 一条成员记录，Version 采用 last-writer-wins 冲突解决。
type Member struct {
	ID      string
	State   MemberState
	Version uint64
}

// ringPosition 将成员 ID 映射到 32 位哈希环上的位置。
func ringPosition(id string) uint32 {
	return crc32.ChecksumIEEE([]byte(id))
}

// bucketOf 返回成员所属的环区间编号（取环位置的高 4 位）。
func bucketOf(id string) int {
	return int(ringPosition(id) >> 28)
}

// memberDigest 计算单条成员记录的 4 字节 CRC 摘要。
func memberDigest(m Member) uint32 {
	h := crc32.NewIEEE()
	h.Write([]byte(m.ID))
	h.Write([]byte{byte(m.State)})
	var vb [8]byte
	binary.LittleEndian.PutUint64(vb[:], m.Version)
	h.Write(vb[:])
	return h.Sum32()
}
