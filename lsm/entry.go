package lsm

// Entry 是 LSM 树中的最小数据单元。
// Tombstone 为 true 时表示这是一条删除标记(墓碑),
// 它会屏蔽所有 Seq 更小的同 Key 旧版本。
type Entry struct {
	Key       string
	Value     []byte
	Seq       uint64 // 全局单调递增序号,越大越新
	Tombstone bool
}

// KV 是 Scan 返回给用户的键值对。
type KV struct {
	Key   string
	Value []byte
}
