package memberlist

// Version 是节点元数据的逻辑版本号，用于并发更新仲裁。
// Epoch 通常取节点本地 Unix 秒（重启后不回退），Seq 为该 Epoch 内的单调计数。
type Version struct {
	Epoch uint64 `json:"epoch"`
	Seq   uint64 `json:"seq"`
}

// Compare 返回 1（v 更新）、0（相等）、-1（v 更旧）。
func (v Version) Compare(o Version) int {
	if v.Epoch != o.Epoch {
		if v.Epoch > o.Epoch {
			return 1
		}
		return -1
	}
	if v.Seq != o.Seq {
		if v.Seq > o.Seq {
			return 1
		}
		return -1
	}
	return 0
}
