package antisync

import (
	"encoding/binary"
	"hash/fnv"
)

// Record 是键值集合中的一条记录。Deleted 为 true 时表示删除墓碑，
// 墓碑同样参与摘要计算，保证删除操作可以随反熵同步传播。
type Record struct {
	Key     uint64
	Value   []byte
	Version uint64
	Deleted bool
}

// hash 计算记录内容的摘要，与记录在树中的位置无关。
func (r Record) hash() uint64 {
	h := fnv.New64a()
	var buf [16]byte
	binary.BigEndian.PutUint64(buf[0:8], r.Key)
	binary.BigEndian.PutUint64(buf[8:16], r.Version)
	h.Write(buf[:])
	if r.Deleted {
		h.Write([]byte{1})
	} else {
		h.Write([]byte{0})
		h.Write(r.Value)
	}
	return h.Sum64()
}

// wins 决定冲突记录中哪一条生效：版本高者优先；版本相同时墓碑优先；
// 再相同则按值字典序取大者，保证所有副本收敛到同一结果。
func wins(incoming, existing Record) bool {
	if incoming.Version != existing.Version {
		return incoming.Version > existing.Version
	}
	if incoming.Deleted != existing.Deleted {
		return incoming.Deleted
	}
	return string(incoming.Value) > string(existing.Value)
}
