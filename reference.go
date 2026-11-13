package netsec

// 完整状态 BFS 参考实现。
//
// 以整张预测表为状态做广度优先展开, 用于独立核对模型性质:
// 同索引异 tag 的替换/衰减行为、置信度边界、模型 B 的域隔离,
// 以及推测窗口的 barrier 语义。FindWitness 的正确性由
// BruteForceWitness(不做状态去重的逐序列枚举)交叉验证。

// Action 是一次可能的训练动作。
type Action struct {
	PC     uint16
	Domain uint8
	Target uint32
}

// ReachableStates 从空表出发, 用完整状态 BFS 展开至多 maxDepth 层
// actions 中的所有动作, 返回全部可达预测器状态(含初始状态)。
func ReachableStates(model Model, actions []Action, maxDepth int) [][TableSize]Entry {
	initial := NewPredictor(model).State()
	seen := map[[TableSize]Entry]bool{initial: true}
	states := [][TableSize]Entry{initial}
	frontier := [][TableSize]Entry{initial}
	for depth := 0; depth < maxDepth; depth++ {
		var next [][TableSize]Entry
		for _, st := range frontier {
			for _, act := range actions {
				p := &Predictor{model: model, table: st}
				p.Train(act.PC, act.Domain, act.Target)
				ns := p.State()
				if seen[ns] {
					continue
				}
				seen[ns] = true
				states = append(states, ns)
				next = append(next, ns)
			}
		}
		frontier = next
	}
	return states
}

// PredictInState 在完整状态 st 上查询预测(参考路径, 不经过 Predictor 对象缓存)。
func PredictInState(model Model, st [TableSize]Entry, pc uint16, domain uint8) (uint32, bool) {
	e := st[Index(model, pc, domain)]
	if e.Valid && e.Tag == Tag(model, pc, domain) && e.Confidence >= PredictThreshold {
		return e.Target, true
	}
	return 0, false
}

// BruteForceWitness 是 FindWitness 的参考实现: 不做状态去重, 按
// (长度, (PC,target) 字典序) 逐个枚举所有训练序列, 返回首个漏洞见证。
// 仅适用于小规模候选集合, 用于交叉验证 BFS 的最短性与字典序。
func BruteForceWitness(in SearchInput) (*Witness, bool) {
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
	level := []*node{root}
	for depth := 0; depth < n.MaxTrain; depth++ {
		var next []*node
		for _, cur := range level {
			for _, pc := range n.TrainPCs {
				for _, tgt := range n.TrainTargets {
					np := cur.pred.Clone()
					ev := np.Train(pc, n.AttackerDomain, tgt)
					events := make([]TrainEvent, len(cur.events)+1)
					copy(events, cur.events)
					events[len(cur.events)] = ev
					if w, ok := n.check(np, events); ok {
						return w, true
					}
					next = append(next, &node{pred: np, events: events})
				}
			}
		}
		level = next
	}
	return nil, false
}
