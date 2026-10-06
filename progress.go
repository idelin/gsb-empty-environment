package aggregator

import "sort"

// Progress 把恢复过程拆成两个互相独立的进度：
//   - Compute：计算状态恢复进度（输入重放/累计值还原到哪）；
//   - Effect：效果确认进度（结果发布出去多少、被确认多少）。
type Progress struct {
	Compute ComputeProgress `json:"compute"`
	Effect  EffectProgress  `json:"effect"`
}

// ComputeProgress 描述计算状态：是否来自检查点恢复、
// 已应用多少输入、各 key 当前累计值。
type ComputeProgress struct {
	RestoredFromCheckpoint bool             `json:"restored_from_checkpoint"`
	InputSeq               int64            `json:"input_seq"`
	Sums                   map[string]int64 `json:"sums"`
}

// EffectProgress 描述效果确认：已结算结果数、待确认数、已确认数。
type EffectProgress struct {
	Emitted    int64    `json:"emitted"`   // 累计结算出的结果数（EmitSeq）
	Pending    int      `json:"pending"`   // 已发布待确认
	Confirmed  int64    `json:"confirmed"` // 已被下游确认
	PendingIDs []string `json:"pending_ids"`
}

// Progress 汇总当前两个进度。
func (a *Aggregator) Progress() Progress {
	a.mu.Lock()
	defer a.mu.Unlock()

	sums := make(map[string]int64, len(a.cp.Sums))
	for k, v := range a.cp.Sums {
		sums[k] = v
	}
	ids := make([]string, 0, len(a.cp.Pending))
	for _, res := range a.cp.Pending {
		ids = append(ids, res.ID)
	}
	sort.Strings(ids)

	return Progress{
		Compute: ComputeProgress{
			RestoredFromCheckpoint: a.restored,
			InputSeq:               a.cp.InputSeq,
			Sums:                   sums,
		},
		Effect: EffectProgress{
			Emitted:    a.cp.EmitSeq,
			Pending:    len(a.cp.Pending),
			Confirmed:  a.cp.Confirmed,
			PendingIDs: ids,
		},
	}
}
