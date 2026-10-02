package checksumdiff

import (
	"encoding/binary"
	"hash/fnv"
)

// Row 表示同步链路中的一行数据。
type Row struct {
	ID        int64 // 主键
	Name      string
	Balance   int64
	UpdatedAt int64
	Version   uint64
}

// Hash 计算整行内容的 64 位摘要（FNV-1a），任一字段变化都会改变结果。
func (r Row) Hash() uint64 {
	h := fnv.New64a()
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(r.ID))
	h.Write(buf[:])
	h.Write([]byte(r.Name))
	h.Write([]byte{0}) // 分隔符，避免拼接歧义
	binary.LittleEndian.PutUint64(buf[:], uint64(r.Balance))
	h.Write(buf[:])
	binary.LittleEndian.PutUint64(buf[:], uint64(r.UpdatedAt))
	h.Write(buf[:])
	binary.LittleEndian.PutUint64(buf[:], r.Version)
	h.Write(buf[:])
	return h.Sum64()
}
