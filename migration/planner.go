package migration

import (
	"sort"

	"prescreen.local/ddd-10554-v3/spec"
)

// beliefNode 是一个信念：与公开观测历史相容的全部具体内部状态集合。
type beliefNode struct {
	key    string
	states map[string]*spec.State
	obsKey string
	// edges[actionFingerprint][observationFingerprint] = 后继信念键。
	// ack 是无信息观测，其分组就是全部后继状态本身；boot 按公开读回分裂。
	edges map[string]map[string]string
}

// Planner 承载一次枚举与求解的全部中间结果。
type Planner struct {
	model   *Model
	nodes   map[string]*beliefNode
	stateOf map[string]*spec.State
	obsOf   map[string]*spec.Observation
	rootKey string
	// solve 阶段产物。
	values []map[string]int
	legal  []map[string][]*spec.Action
	choice map[string]map[int]*policyChoice
}

// Solve 对芯片做完整信念枚举并求解最小最坏烧录策略。
func Solve(chip *spec.Chip) (*Planner, error) {
	model, err := New(chip)
	if err != nil {
		return nil, err
	}
	p := &Planner{
		model:   model,
		nodes:   map[string]*beliefNode{},
		stateOf: map[string]*spec.State{},
		obsOf:   map[string]*spec.Observation{},
	}
	if err := p.enumerate(); err != nil {
		return nil, err
	}
	if err := p.solve(); err != nil {
		return nil, err
	}
	return p, nil
}

func stateFingerprint(s *spec.State) string {
	k, err := spec.StateKey(s)
	if err != nil {
		panic(err)
	}
	return k
}

func actionFingerprint(a *spec.Action) string {
	k, err := spec.ActionKey(a)
	if err != nil {
		panic(err)
	}
	return k
}

func observationFingerprint(o *spec.Observation) string {
	k, err := spec.ObservationKey(o)
	if err != nil {
		panic(err)
	}
	return k
}

// beliefFingerprint 用排序后的具体状态指纹集合定义信念身份。
// 信念是 POMDP 的充分统计量：相同相容状态集合意味着宿主对芯片的
// 认知完全相同，无论沿哪条观测历史到达都必须给出相同动作。
func beliefFingerprint(states map[string]*spec.State) string {
	keys := make([]string, 0, len(states))
	for k := range states {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fp, err := spec.Fingerprint(keys)
	if err != nil {
		panic(err)
	}
	return fp
}

// intersectActions 计算信念中所有状态在给定预算下都合法的动作交集。
func (p *Planner) intersectActions(n *beliefNode, budgetLeft int) []*spec.Action {
	count := map[string]int{}
	first := map[string]*spec.Action{}
	total := 0
	for _, s := range n.states {
		total++
		for _, a := range p.model.LegalActions(s, budgetLeft) {
			k := actionFingerprint(a)
			count[k]++
			if _, ok := first[k]; !ok {
				first[k] = a
			}
		}
	}
	var keys []string
	for k, c := range count {
		if c == total {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := make([]*spec.Action, 0, len(keys))
	for _, k := range keys {
		out = append(out, first[k])
	}
	return out
}

func (p *Planner) allTerminal(n *beliefNode) bool {
	for _, s := range n.states {
		if !p.model.Terminal(s) {
			return false
		}
	}
	return true
}

// terminalReachable 检查信念图上是否存在从根信念到终止信念的安全路径。
func (p *Planner) terminalReachable() bool {
	seen := map[string]bool{p.rootKey: true}
	queue := []string{p.rootKey}
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		n := p.nodes[k]
		if p.allTerminal(n) {
			return true
		}
		for _, byObs := range n.edges {
			for _, child := range byObs {
				if !seen[child] {
					seen[child] = true
					queue = append(queue, child)
				}
			}
		}
	}
	return false
}

// terminalDistances 计算每个动作（在其所属信念语境下取该动作所有后继
// 信念中到终止信念的最小 BFS 边距）。边距越小表示动作越直接推进迁移，
// 用于在等烧录代价的动作间打破零代价自环平局。
//
// 返回 map[beliefKey]map[actionKey]int 更精确，这里按 (信念,动作) 编码。
func (p *Planner) terminalDistances() map[string]int {
	// 先在信念图（忽略动作类型）上反向 BFS，求每个信念到任一终止信念
	// 的最短边数。
	dist := map[string]int{}
	var queue []string
	for k, n := range p.nodes {
		if p.allTerminal(n) {
			dist[k] = 0
			queue = append(queue, k)
		}
	}
	reverse := map[string][]string{}
	for k, n := range p.nodes {
		for _, byObs := range n.edges {
			for _, child := range byObs {
				reverse[child] = append(reverse[child], k)
			}
		}
	}
	for head := 0; head < len(queue); head++ {
		cur := queue[head]
		for _, parent := range reverse[cur] {
			if _, ok := dist[parent]; !ok {
				dist[parent] = dist[cur] + 1
				queue = append(queue, parent)
			}
		}
	}

	// 再把信念边距折算为（信念,动作）键，取该动作最差后继的边距，
	// 保证环境选择任一观测分支时都在朝终止推进。
	out := map[string]int{}
	for k, n := range p.nodes {
		for ak, byObs := range n.edges {
			worst := 0
			for _, child := range byObs {
				d, ok := dist[child]
				if !ok {
					d = 1 << 20
				}
				if d > worst {
					worst = d
				}
			}
			out[k+"|"+ak] = worst
		}
	}
	return out
}

// enumerate 做信念空间 BFS。信念身份是当前相容具体状态集合的指纹：
// 动作后的每个观测把后继状态分组，同组即同一信念；有限的具体状态空间
// 决定了不同信念数量有限，BFS 必然终止。
func (p *Planner) enumerate() error {
	rootStates := map[string]*spec.State{
		stateFingerprint(p.model.chip.Initial): cloneState(p.model.chip.Initial),
	}
	p.stateOf[stateFingerprint(p.model.chip.Initial)] = cloneState(p.model.chip.Initial)
	for _, alt := range p.model.chip.InitialAlternatives {
		ak := stateFingerprint(alt)
		rootStates[ak] = cloneState(alt)
		p.stateOf[ak] = cloneState(alt)
	}
	rootHist := beliefFingerprint(rootStates)
	root := &beliefNode{
		key:    rootHist,
		states: rootStates,
		obsKey: "",
		edges:  map[string]map[string]string{},
	}
	p.nodes[root.key] = root
	p.rootKey = root.key

	queue := []string{root.key}
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		n := p.nodes[key]
		if p.allTerminal(n) {
			continue
		}
		// 动作交集：必须在信念的每个相容状态上都合法且安全可执行。
		candidates := p.intersectActions(n, p.model.chip.MaxBurns)
		for _, action := range candidates {
			ak := actionFingerprint(action)
			allOK := true
			groups := map[string]map[string]*spec.State{}
			for _, s := range n.states {
				outcomes, err := p.model.Outcomes(s, action)
				if err != nil {
					allOK = false
					break
				}
				for _, oc := range outcomes {
					stateSK := stateFingerprint(oc.State)
					p.stateOf[stateSK] = cloneState(oc.State)
					okFP := observationFingerprint(oc.Observation)
					if _, seen := p.obsOf[okFP]; !seen {
						p.obsOf[okFP] = oc.Observation
					}
					if groups[okFP] == nil {
						groups[okFP] = map[string]*spec.State{}
					}
					if _, seen := groups[okFP][stateSK]; !seen {
						groups[okFP][stateSK] = cloneState(oc.State)
					}
				}
			}
			if !allOK {
				continue
			}
			n.edges[ak] = map[string]string{}
			for okFP, group := range groups {
				childHist := beliefFingerprint(group)
				child, existed := p.nodes[childHist]
				if !existed {
					child = &beliefNode{
						key:    childHist,
						states: map[string]*spec.State{},
						obsKey: okFP,
						edges:  map[string]map[string]string{},
					}
					p.nodes[childHist] = child
				}
				for stateSK, st := range group {
					if _, ok := child.states[stateSK]; !ok {
						child.states[stateSK] = st
					}
				}
				n.edges[ak][okFP] = child.key
				if !existed && !p.allTerminal(child) {
					queue = append(queue, child.key)
				}
			}
		}
	}

	return nil
}
