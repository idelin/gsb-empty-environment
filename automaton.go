package netsec

import "sort"

// Target 是有限目标域中的一个元素，例如主机地址或端口的字符串标识。
type Target = string

// TransKey 是自动机迁移表的键：源状态加上本时间段实际选择的目标。
type TransKey struct {
	State  int
	Target Target
}

// NFA 是一个运行在有限目标域上的非确定性有限自动机，用于描述扫描模式。
//
// 自动机在每一个时间段消费一个目标：该时间段内只要补全后的尝试集合中
// 存在某个目标，能让自动机从当前状态集合沿迁移继续推进即可。迁移表中
// 没有列出的 (状态, 目标) 组合表示不存在该迁移。NFA 不支持 epsilon 迁移。
//
// 当且仅当处理完全部时间段后状态集合与 Accept 相交时，判定该模式发生。
type NFA struct {
	// Start 是初始状态集合。
	Start []int
	// Accept 是接受状态集合。
	Accept []int
	// Trans 按 (源状态, 目标) 列出所有合法的下一状态；同一键可有多个值。
	Trans map[TransKey][]int
}

// NewNFA 用初始状态、接受状态和迁移构造自动机，迁移项会被复制并排序去重。
func NewNFA(start, accept []int, trans map[TransKey][]int) *NFA {
	copied := make(map[TransKey][]int, len(trans))
	for key, next := range trans {
		dup := append([]int(nil), next...)
		sort.Ints(dup)
		copied[key] = compactInts(dup)
	}
	return &NFA{
		Start:  append([]int(nil), start...),
		Accept: append([]int(nil), accept...),
		Trans:  copied,
	}
}

// AddTransition 增加一条从 state 出发、消费 target 后到达 next 的迁移。
func (n *NFA) AddTransition(state int, target Target, next int) {
	if n.Trans == nil {
		n.Trans = make(map[TransKey][]int)
	}
	key := TransKey{State: state, Target: target}
	n.Trans[key] = compactInts(append(n.Trans[key], next))
}

// validate 检查自动机本身结构，并保证所有迁移引用的目标都属于目标域。
func (n *NFA) validate(domain []Target) error {
	if n == nil {
		return ErrNilAutomaton
	}
	if len(n.Start) == 0 {
		return ErrEmptyStart
	}
	if len(n.Accept) == 0 {
		return ErrEmptyAccept
	}
	inDomain := make(map[Target]bool, len(domain))
	for _, target := range domain {
		inDomain[target] = true
	}
	for key := range n.Trans {
		if !inDomain[key.Target] {
			return ErrTargetOutsideDomain
		}
	}
	return nil
}

// backpointer 记录状态集合中某个状态是由哪一个父状态、经哪一个目标到达的，
// 用于在判定接受/拒绝后回溯出具体的补全见证路径。
type backpointer struct {
	parent int
	target Target
}

// runLayer 保存一个时间段处理后的状态集合及对应回溯指针。
type runLayer struct {
	states map[int]backpointer
}

// runResult 是自动机对一条完整补全序列的判定结果。
type runResult struct {
	accepted bool
	// layers[i] 对应第 i 个时间段处理后的状态集合。
	layers []runLayer
	// startState 是回溯起点对应的初始状态。
	startState int
	// endState 是最后一个时间段落在接受集合（或拒绝时任意可达状态）中的状态。
	endState int
}

// evaluate 让自动机按给定的每段尝试集合运行。
//
// 每一段从当前状态集合出发，尝试集合里的任意目标都可驱动迁移，体现
// “该时间段内多个连接尝试的先后顺序不被区分”的弱观测语义。
func (n *NFA) evaluate(segments [][]Target) runResult {
	current := make(map[int]struct{}, len(n.Start))
	for _, state := range n.Start {
		current[state] = struct{}{}
	}

	layers := make([]runLayer, 0, len(segments))
	for _, choices := range segments {
		next := make(map[int]backpointer)
		for state := range current {
			for _, target := range choices {
				for _, dest := range n.Trans[TransKey{State: state, Target: target}] {
					if _, seen := next[dest]; !seen {
						next[dest] = backpointer{parent: state, target: target}
					}
				}
			}
		}
		layers = append(layers, runLayer{states: next})
		current = make(map[int]struct{}, len(next))
		for state := range next {
			current[state] = struct{}{}
		}
	}

	result := runResult{layers: layers}
	accept := make(map[int]bool, len(n.Accept))
	for _, state := range n.Accept {
		accept[state] = true
	}

	var endState int
	var found bool
	for state := range current {
		if accept[state] {
			endState = state
			found = true
			break
		}
	}

	// 拒绝时也需要一个可达状态用于回溯见证。
	if !found {
		for state := range current {
			endState = state
			found = true
			break
		}
	}
	result.accepted = found && accept[endState]
	result.endState = endState

	// 从终点回溯到初始状态，确定路径使用的初始状态。若自动机在某一段
	// 后进入空状态集合（彻底走死），回溯在该段终止，剩余段留空目标。
	state := endState
	for index := len(layers) - 1; index >= 0; index-- {
		pointer, ok := layers[index].states[state]
		if !ok {
			break
		}
		state = pointer.parent
	}
	result.startState = state
	return result
}

// reconstructPath 依据回溯指针还原自动机实际选择的逐段目标。
// 自动机走死的时间段对应空字符串，仅可能出现在拒绝见证中。
func reconstructPath(result runResult) []Target {
	path := make([]Target, len(result.layers))
	state := result.endState
	for index := len(result.layers) - 1; index >= 0; index-- {
		pointer, ok := result.layers[index].states[state]
		if !ok {
			break
		}
		path[index] = pointer.target
		state = pointer.parent
	}
	return path
}

// compactInts 对已排序的整数切片去重。
func compactInts(values []int) []int {
	if len(values) <= 1 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}
