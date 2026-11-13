package netsec

import "sort"

// 见证搜索: 寻找使"架构目标安全但 probe 轨迹出现"的最短训练序列。
//
// 攻击者在域 AttackerDomain 执行至多 MaxTrain 条 (trainPC, target) 训练,
// trainPC 与 target 均取自调用方给定的有限集合。受害者在 VictimPC 执行
// 一次间接分支, 架构真实目标 ArchTarget 来自宿主白名单。
//
// 漏洞判定: 架构目标安全(在白名单且不在危险集合; 架构真实调用危险目标
// 不算预测器漏洞), 且预测目标不同、属于危险集合, 且推测窗口产生非空
// probe 轨迹。
//
// 等长序列按 (PC, target) 字典序取最小者。

const (
	DefaultMaxTrain   = 8
	DefaultMaxSpecOps = 6
)

// SearchInput 是见证搜索的输入。
type SearchInput struct {
	Model          Model
	VictimPC       uint16
	VictimDomain   uint8
	AttackerDomain uint8
	ArchTarget     uint32              // 架构真实目标, 必须来自白名单
	TrainPCs       []uint16            // 训练 PC 的有限候选集合
	TrainTargets   []uint32            // 训练目标的有限候选集合
	Whitelist      []uint32            // 宿主白名单目标
	Dangerous      []uint32            // 宿主危险目标集合
	DangerPrograms map[uint32][]SpecOp // 危险目标的推测操作序列
	MaxTrain       int                 // 训练条数上限, <=0 时取 8
	MaxSpecOps     int                 // 推测窗口操作上限, <=0 时取 6
}

// Witness 是一条攻击见证: 训练事件序列与受害者执行结果。
type Witness struct {
	Model  Model
	Events []TrainEvent // 每条事件含 tag、索引与置信度变化
	Run    VictimRun
}

// Sequence 返回见证的 (PC, target) 训练序列, 用于字典序比较。
func (w *Witness) Sequence() [][2]uint32 {
	seq := make([][2]uint32, len(w.Events))
	for i, ev := range w.Events {
		seq[i] = [2]uint32{uint32(ev.PC), ev.Target}
	}
	return seq
}

type normalizedInput struct {
	SearchInput
	whiteSet  map[uint32]bool
	dangerSet map[uint32]bool
}

func (in SearchInput) normalize() normalizedInput {
	n := normalizedInput{SearchInput: in}
	if n.MaxTrain <= 0 {
		n.MaxTrain = DefaultMaxTrain
	}
	if n.MaxSpecOps <= 0 {
		n.MaxSpecOps = DefaultMaxSpecOps
	}
	n.TrainPCs = sortedUnique16(in.TrainPCs)
	n.TrainTargets = sortedUnique32(in.TrainTargets)
	n.whiteSet = makeSet(in.Whitelist)
	n.dangerSet = makeSet(in.Dangerous)
	return n
}

// archSafe 报告架构真实目标是否安全。架构真实调用危险目标属于宿主自身
// 行为, 不算预测器漏洞。
func (n normalizedInput) archSafe() bool {
	return n.whiteSet[n.ArchTarget] && !n.dangerSet[n.ArchTarget]
}

// vulnerable 判定一次受害者执行是否构成预测器漏洞。
func (n normalizedInput) vulnerable(run VictimRun) bool {
	if !n.archSafe() {
		return false
	}
	if !run.Mispredicted || len(run.ProbeTrace) == 0 {
		return false
	}
	return n.dangerSet[run.PredictedTarget]
}

// check 在给定预测器状态下执行受害者分支并判定漏洞。
func (n normalizedInput) check(p *Predictor, events []TrainEvent) (*Witness, bool) {
	run := RunVictim(p, n.VictimPC, n.VictimDomain, n.ArchTarget,
		func(target uint32) []SpecOp { return n.DangerPrograms[target] },
		n.MaxSpecOps)
	if n.vulnerable(run) {
		return &Witness{Model: n.Model, Events: events, Run: run}, true
	}
	return nil, false
}

// FindWitness 用 BFS 搜索最短攻击见证。候选集合先排序, 按 (长度, 字典序)
// 展开, 并对完整预测器状态去重, 因此首个命中即最短且字典序最小。
func FindWitness(in SearchInput) (*Witness, bool) {
	n := in.normalize()
	if !n.archSafe() {
		return nil, false
	}
	type node struct {
		pred   *Predictor
		events []TrainEvent
	}
	root := &node{pred: NewPredictor(n.Model)}
	if w, ok := n.check(root.pred, nil); ok {
		return w, true
	}
	seen := map[[TableSize]Entry]bool{root.pred.State(): true}
	queue := []*node{root}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if len(cur.events) >= n.MaxTrain {
			continue
		}
		for _, pc := range n.TrainPCs {
			for _, tgt := range n.TrainTargets {
				np := cur.pred.Clone()
				ev := np.Train(pc, n.AttackerDomain, tgt)
				state := np.State()
				if seen[state] {
					continue
				}
				seen[state] = true
				events := make([]TrainEvent, len(cur.events)+1)
				copy(events, cur.events)
				events[len(cur.events)] = ev
				if w, ok := n.check(np, events); ok {
					return w, true
				}
				queue = append(queue, &node{pred: np, events: events})
			}
		}
	}
	return nil, false
}

// ModelResult 是单个模型的搜索结果。
type ModelResult struct {
	Model   Model
	Witness *Witness // nil 表示在给定集合与长度上限内无见证
	Note    string
}

// CompareModels 分别报告模型 A 与模型 B 的结果。
// 模型 B 的"无见证"结论只对给定候选集合与长度上限成立。
func CompareModels(in SearchInput) (a, b ModelResult) {
	inA := in
	inA.Model = ModelA
	a.Model = ModelA
	if w, ok := FindWitness(inA); ok {
		a.Witness = w
	} else {
		a.Note = "no witness within given PC/target sets and max length"
	}
	inB := in
	inB.Model = ModelB
	b.Model = ModelB
	if w, ok := FindWitness(inB); ok {
		b.Witness = w
	} else {
		b.Note = "no witness within given PC/target sets and max length"
	}
	return a, b
}

func sortedUnique16(xs []uint16) []uint16 {
	out := append([]uint16(nil), xs...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	k := 0
	for _, x := range out {
		if k == 0 || out[k-1] != x {
			out[k] = x
			k++
		}
	}
	return out[:k]
}

func sortedUnique32(xs []uint32) []uint32 {
	out := append([]uint32(nil), xs...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	k := 0
	for _, x := range out {
		if k == 0 || out[k-1] != x {
			out[k] = x
			k++
		}
	}
	return out[:k]
}

func makeSet(xs []uint32) map[uint32]bool {
	s := make(map[uint32]bool, len(xs))
	for _, x := range xs {
		s[x] = true
	}
	return s
}
