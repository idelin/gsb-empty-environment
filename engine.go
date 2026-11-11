package netsec

// edgeRec 记录一条抽象可达边，供反例路径重建使用。
type edgeRec struct {
	fromNode int
	fromCube abstractState
}

// reachRec 是某节点上一个可达抽象状态的可达性记录。
type reachRec struct {
	pred edgeRec
}

// summaryEntry 是节点级摘要的一条：入口抽象立方对应的出口立方集合，
// 以及可触发错误节点的入口立方集合。
type summaryEntry struct {
	outs    []abstractState
	failing []abstractState
}

// engine 持有一次 Verify 调用的全部可变状态。
type engine struct {
	prog       *Program
	nvars      int
	universe   []Valuation
	predicates []Expr
	summaries  map[int]map[abstractState]*summaryEntry
	enumUsed   int
	refines    int
	rebuilt    int
	reencoded  int
	events     []Event
	opts       Options
}

func newEngine(prog *Program, initial []Expr, opts Options) *engine {
	return &engine{
		prog:       prog,
		nvars:      len(prog.Vars),
		universe:   prog.universe(),
		predicates: dedupPredicates(initial),
		summaries:  make(map[int]map[abstractState]*summaryEntry),
		opts:       opts,
	}
}

// charge 登记一次具体取值枚举，超出预算由主循环统一裁决。
func (e *engine) charge(n int) { e.enumUsed += n }

// initialStates 枚举入口处全部可能取值；程序未显式给出时即整个具体状态空间。
func (e *engine) initialStates() []Valuation {
	if e.prog.Init != nil {
		return e.prog.Init
	}
	return e.universe
}

// abstractOf 把一组具体取值映射为当前谓词下的抽象立方。
func (e *engine) abstractOf(set valSet) abstractState {
	var cube abstractState
	for i, pred := range e.predicates {
		allTrue, allFalse := true, true
		for s := range set {
			if pred.eval(s) {
				allFalse = false
			} else {
				allTrue = false
			}
		}
		if allTrue {
			cube = cube.setBit(i, true)
		} else if allFalse {
			cube = cube.setBit(i, false)
		}
	}
	return cube
}

// cubeMembers 枚举满足抽象立方的全部具体取值。
func (e *engine) cubeMembers(cube abstractState) []Valuation {
	out := make([]Valuation, 0)
	for _, s := range e.universe {
		if cube.contains(s, e.predicates) {
			out = append(out, s)
		}
	}
	return out
}

// transformer 返回（必要时计算并缓存）节点对入口立方的抽象摘要。
func (e *engine) transformer(nodeIdx int, cube abstractState) *summaryEntry {
	nodeSum := e.summaries[nodeIdx]
	if nodeSum == nil {
		nodeSum = make(map[abstractState]*summaryEntry)
		e.summaries[nodeIdx] = nodeSum
	}
	if entry, ok := nodeSum[cube]; ok {
		return entry
	}

	members := e.cubeMembers(cube)
	e.charge(len(members))
	entry := &summaryEntry{}
	outs := make(valSet)
	fail := make(valSet)
	node := e.prog.Nodes[nodeIdx]
	for _, s := range members {
		succs, fails := concreteSuccs(node, s)
		for _, ns := range succs {
			outs.add(ns)
		}
		if fails {
			fail.add(s)
		}
	}
	if len(outs) > 0 {
		entry.outs = []abstractState{e.abstractOf(outs)}
	}
	if len(fail) > 0 {
		entry.failing = []abstractState{e.abstractOf(fail)}
	}
	nodeSum[cube] = entry
	return entry
}

type workItem struct {
	node int
	cube abstractState
}

// abstractReachability 在当前谓词集合上做不动点可达分析，
// 返回错误前驱节点与入口立方；不可达时 errPred 为 -1，并给出重建路径。
func (e *engine) abstractReachability() (errPred int, errCube abstractState, path []int) {
	reached := make(map[int]map[abstractState]reachRec)

	initial := make(valSet)
	for _, s := range e.initialStates() {
		initial.add(s)
	}
	entryCube := e.abstractOf(initial)
	reached[e.prog.Entry] = map[abstractState]reachRec{
		entryCube: {pred: edgeRec{fromNode: -1}},
	}
	worklist := []workItem{{node: e.prog.Entry, cube: entryCube}}

	errPred = -1
	for len(worklist) > 0 {
		item := worklist[0]
		worklist = worklist[1:]
		node := e.prog.Nodes[item.node]
		entry := e.transformer(item.node, item.cube)

		if node.ErrTarget && len(entry.failing) > 0 {
			errPred, errCube = item.node, item.cube
		}
		for _, out := range entry.outs {
			for _, t := range node.Next {
				nodeReached := reached[t]
				if nodeReached == nil {
					nodeReached = make(map[abstractState]reachRec)
					reached[t] = nodeReached
				}
				if _, ok := nodeReached[out]; ok {
					continue
				}
				nodeReached[out] = reachRec{pred: edgeRec{fromNode: item.node, fromCube: item.cube}}
				worklist = append(worklist, workItem{node: t, cube: out})
			}
		}
	}

	if errPred != -1 {
		path = e.reconstructPath(reached, errPred, errCube)
	}
	return errPred, errCube, path
}

// reconstructPath 沿前驱指针重建通往错误节点的抽象反例路径。
func (e *engine) reconstructPath(reached map[int]map[abstractState]reachRec, errPred int, errCube abstractState) []int {
	type frame struct {
		node int
		cube abstractState
	}
	frames := []frame{{node: errPred, cube: errCube}}
	for {
		rec := reached[frames[0].node][frames[0].cube]
		if rec.pred.fromNode == -1 {
			break
		}
		frames = append([]frame{{node: rec.pred.fromNode, cube: rec.pred.fromCube}}, frames...)
	}
	path := make([]int, len(frames))
	for i, f := range frames {
		path[i] = f.node
	}
	return path
}
