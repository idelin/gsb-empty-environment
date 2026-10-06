package aggregator

// Result 是聚合器对外发布的一条结果。
// ID 在结果首次生成时确定并持久化进检查点，
// 恢复后重发必须沿用同一 ID，绝不重新生成。
type Result struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Value int64  `json:"value"`
}

// Checkpoint 是聚合器崩溃恢复的唯一依据，包含两类状态：
//  1. 计算状态：累计值 Sums / 已结算基线 Emitted / 输入进度 InputSeq；
//  2. 待发结果：已生成但尚未被接收方确认的 Pending 列表（含稳定 ID）。
type Checkpoint struct {
	Sums      map[string]int64 `json:"sums"`      // 每个 key 的累计值
	Emitted   map[string]int64 `json:"emitted"`   // 每个 key 已结算进结果的累计基线
	Pending   []Result         `json:"pending"`   // 已发布未确认的结果（outbox）
	EmitSeq   int64            `json:"emit_seq"`  // 结果 ID 序号，单调递增且持久化
	InputSeq  int64            `json:"input_seq"` // 已应用的输入事件数
	Confirmed int64            `json:"confirmed"` // 累计已确认结果数
}

func newCheckpoint() Checkpoint {
	return Checkpoint{
		Sums:    make(map[string]int64),
		Emitted: make(map[string]int64),
	}
}

func (cp Checkpoint) clone() Checkpoint {
	out := cp
	out.Sums = make(map[string]int64, len(cp.Sums))
	for k, v := range cp.Sums {
		out.Sums[k] = v
	}
	out.Emitted = make(map[string]int64, len(cp.Emitted))
	for k, v := range cp.Emitted {
		out.Emitted[k] = v
	}
	out.Pending = append([]Result(nil), cp.Pending...)
	return out
}
