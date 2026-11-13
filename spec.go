package netsec

// 推测窗口模拟。
//
// 受害者在固定 PC 执行一次间接分支。若预测目标与架构真实目标不同,
// 且预测目标属于宿主危险目标集合, 则进入推测窗口: 顺序记录最多
// MaxSpecOps 条推测操作, barrier 操作立即停止窗口。窗口结束时所有
// 寄存器写回滚, 但 probe(address) 产生的观察保留在轨迹中。

// OpKind 是推测操作的种类。
type OpKind int

const (
	OpWrite   OpKind = iota // 寄存器写, 窗口结束时回滚
	OpProbe                 // probe(address): 观察保留
	OpBarrier               // barrier: 停止推测窗口
)

// SpecOp 是一条推测操作。
type SpecOp struct {
	Kind OpKind
	Addr uint32 // 仅 OpProbe 使用
}

func Write() SpecOp            { return SpecOp{Kind: OpWrite} }
func Probe(addr uint32) SpecOp { return SpecOp{Kind: OpProbe, Addr: addr} }
func Barrier() SpecOp          { return SpecOp{Kind: OpBarrier} }

// VictimRun 是受害者一次间接分支的执行结果。
type VictimRun struct {
	ArchTarget      uint32   // 架构真实目标(来自宿主白名单)
	Predicted       bool     // 预测器是否给出预测
	PredictedTarget uint32   // 预测目标
	Mispredicted    bool     // 预测目标与架构目标不同
	SpecOps         []SpecOp // 推测窗口内记录的操作(不含 barrier)
	ProbeTrace      []uint32 // 窗口内 probe 观察序列(回滚后仍保留)
	BarrierHit      bool     // 窗口是否被 barrier 停止
}

// RunVictim 模拟受害者在 victimPC 处执行一次间接分支。
// program 给出预测目标对应的推测操作序列(宿主危险目标的微码);
// 最多记录 maxSpecOps 条操作, 遇 barrier 提前停止。
func RunVictim(p *Predictor, victimPC uint16, victimDomain uint8, archTarget uint32, program func(target uint32) []SpecOp, maxSpecOps int) VictimRun {
	run := VictimRun{ArchTarget: archTarget}
	pred, ok := p.Predict(victimPC, victimDomain)
	if !ok {
		return run
	}
	run.Predicted = true
	run.PredictedTarget = pred
	if pred == archTarget {
		return run
	}
	run.Mispredicted = true
	for _, op := range program(pred) {
		if len(run.SpecOps) >= maxSpecOps {
			break
		}
		if op.Kind == OpBarrier {
			run.BarrierHit = true
			break
		}
		run.SpecOps = append(run.SpecOps, op)
		if op.Kind == OpProbe {
			run.ProbeTrace = append(run.ProbeTrace, op.Addr)
		}
	}
	// 窗口结束: 所有 OpWrite 的寄存器写回滚(模型中不落任何状态),
	// 仅 ProbeTrace 中的观察保留。
	return run
}
