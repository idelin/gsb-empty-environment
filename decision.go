package weightedvote

import "fmt"

// Decision 是合并汇总后的最终定论。
type Decision int

const (
	// Pending 表示尚无定论（有效权重不足，或赞成/反对均未达到门槛）。
	Pending Decision = iota
	// Approved 表示赞成票权重超过全网有效总权重的一半。
	Approved
	// Rejected 表示反对票权重达到全网有效总权重的一半（弃权抬高通过门槛）。
	Rejected
)

func (d Decision) String() string {
	switch d {
	case Approved:
		return "Approved"
	case Rejected:
		return "Rejected"
	default:
		return "Pending"
	}
}

// Tally 是一次状态汇总的结果。
type Tally struct {
	Voters        int    // 有效投票人数（有票且有权重）
	TotalWeight   uint64 // 全网有效总权重（含弃权）
	YesWeight     uint64 // 赞成票权重
	NoWeight      uint64 // 反对票权重
	AbstainWeight uint64 // 弃权票权重
}

// Decision 按确定性规则给出最终定论：
//   - 赞成权重严格超过有效总权重的一半 -> Approved
//   - 反对权重达到有效总权重的一半     -> Rejected
//   - 否则                             -> Pending
//
// 弃权票计入总权重，因此抬高了两边的门槛；规则只依赖汇总数值，输出确定。
func (t Tally) Decision() Decision {
	if t.TotalWeight == 0 {
		return Pending
	}
	if t.YesWeight*2 > t.TotalWeight {
		return Approved
	}
	if t.NoWeight*2 >= t.TotalWeight {
		return Rejected
	}
	return Pending
}

func (t Tally) String() string {
	return fmt.Sprintf("voters=%d total=%d yes=%d no=%d abstain=%d => %s",
		t.Voters, t.TotalWeight, t.YesWeight, t.NoWeight, t.AbstainWeight, t.Decision())
}
