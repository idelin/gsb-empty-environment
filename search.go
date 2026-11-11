package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// DefaultMaxStates 是状态枚举的默认上限，防止状态空间爆炸时失控。
const DefaultMaxStates = 5_000_000

// StepTrace 记录交错中的一步及其在两种语义下的结果。
type StepTrace struct {
	Thread   int
	Op       Op
	IsCAS    bool
	RawOK    bool // 裸地址 CAS 是否成功（仅 IsCAS 时有意义）
	TaggedOK bool // 带代次 CAS 是否成功（仅 IsCAS 时有意义）
	Diverged bool // 本步是否为两种语义的首次分歧
	Lost     []int
}

// Witness 是首次分歧且造成事件丢失的交错证据。
type Witness struct {
	Steps []StepTrace
	Lost  []int // 分歧步实际丢失的事件 ID
}

// Length 返回交错的步数。
func (w *Witness) Length() int { return len(w.Steps) }

func casWord(ok bool) string {
	if ok {
		return "成功"
	}
	return "失败"
}

// String 输出人类可读的证据，包含每步调度、两种 CAS 结果与丢失的事件 ID。
func (w *Witness) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "首次分歧且丢失事件的最短交错（%d 步）:\n", len(w.Steps))
	for i, s := range w.Steps {
		fmt.Fprintf(&b, "  %2d. T%d %-14s", i+1, s.Thread, s.Op)
		if s.IsCAS {
			fmt.Fprintf(&b, "  裸地址CAS=%s 带代次CAS=%s", casWord(s.RawOK), casWord(s.TaggedOK))
		}
		if s.Diverged {
			b.WriteString("  <<< 首次分歧")
		}
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "丢失的事件 ID: %v\n", w.Lost)
	return b.String()
}

// pair 是同一调度下两种语义的状态对。
type pair struct {
	raw *machine
	tag *machine
}

func (p *pair) key() string {
	var b strings.Builder
	writeMachine := func(m *machine) {
		fmt.Fprintf(&b, "h%d.%d|", m.head.Slot, m.head.Gen)
		for _, s := range m.slots {
			fmt.Fprintf(&b, "%d.%d.%d.%d,", s.gen, s.event, s.next.Slot, s.next.Gen)
		}
		b.WriteByte('|')
		for _, f := range m.free {
			fmt.Fprintf(&b, "%d,", f)
		}
		b.WriteByte('|')
		for t := range m.pc {
			fmt.Fprintf(&b, "%d:", m.pc[t])
			names := make([]string, 0, len(m.regs[t]))
			for n := range m.regs[t] {
				names = append(names, n)
			}
			sort.Strings(names)
			for _, n := range names {
				r := m.regs[t][n]
				fmt.Fprintf(&b, "%s=%d.%d;", n, r.Slot, r.Gen)
			}
			b.WriteByte(',')
		}
		b.WriteByte('#')
	}
	writeMachine(p.raw)
	writeMachine(p.tag)
	return b.String()
}

// divergenceLoss 计算分歧步在裸地址语义下丢失、而带代次语义保住的事件 ID。
// rawBefore/rawAfter 是裸地址机器在分歧步前后的状态，tagAfter 是带代次机器在分歧步后的状态。
func divergenceLoss(rawBefore, rawAfter, tagAfter *machine) []int {
	before := rawBefore.reachableEvents()
	afterRaw := rawAfter.reachableEvents()
	afterTag := tagAfter.reachableEvents()
	var lost []int
	for ev := range before {
		if !afterRaw[ev] && afterTag[ev] {
			lost = append(lost, ev)
		}
	}
	sort.Ints(lost)
	return lost
}

// LossRecord 是完整枚举中一条首次分歧即丢事件的交错记录。
type LossRecord struct {
	Length int
	Lost   []int
}

// Result 是状态枚举的结果。
type Result struct {
	// Witness 是最短的首次分歧且丢事件交错；不存在时为 nil。
	Witness *Witness
	// Losses 是完整枚举到的所有首次分歧丢事件记录（按长度升序）。
	Losses []LossRecord
	// Explored 是枚举去重后的状态数。
	Explored int
	// DivergentLossless 是首次分歧但未丢事件而被剪枝的分支数。
	DivergentLossless int
	// ReverseDivergences 是“带代次成功而裸地址失败”的反向分歧数，正常应为 0。
	ReverseDivergences int
	// Truncated 表示是否因达到状态上限而提前终止。
	Truncated bool
}

type bfsNode struct {
	st     *pair
	parent int // 在 nodes 中的父节点下标，-1 表示根
	step   StepTrace
}

// enumerate 对脚本做完整状态枚举（BFS，按交错长度分层）。
// 分歧但未丢事件的分支被剪枝：其“首次分歧”已发生且不丢事件，
// 与“首次分歧即丢事件”的目标无关。stopAtFirst 为真时找到最短见证即返回。
func enumerate(scripts [][]Op, stopAtFirst bool, maxStates int) (*Result, error) {
	if err := Validate(scripts); err != nil {
		return nil, err
	}
	if maxStates <= 0 {
		maxStates = DefaultMaxStates
	}
	n := len(scripts)
	root := &pair{raw: newMachine(n), tag: newMachine(n)}
	res := &Result{}
	nodes := []bfsNode{{st: root, parent: -1}}
	visited := map[string]bool{root.key(): true}
	queue := []int{0}

	buildWitness := func(idx int) *Witness {
		var steps []StepTrace
		for i := idx; i > 0; i = nodes[i].parent {
			steps = append(steps, nodes[i].step)
		}
		for l, r := 0, len(steps)-1; l < r; l, r = l+1, r-1 {
			steps[l], steps[r] = steps[r], steps[l]
		}
		return &Witness{Steps: steps, Lost: steps[len(steps)-1].Lost}
	}

	for head := 0; head < len(queue); head++ {
		cur := nodes[queue[head]]
		for t := 0; t < n; t++ {
			pc := cur.st.raw.pc[t]
			if pc >= len(scripts[t]) {
				continue
			}
			op := scripts[t][pc]
			child := &pair{raw: cur.st.raw.clone(), tag: cur.st.tag.clone()}
			child.raw.pc[t]++
			child.tag.pc[t]++
			step := StepTrace{Thread: t, Op: op}
			if op.Kind == OpCAS {
				step.IsCAS = true
				step.RawOK = child.raw.apply(t, op, RawAddr)
				step.TaggedOK = child.tag.apply(t, op, Tagged)
				if step.RawOK != step.TaggedOK {
					step.Diverged = true
					if !step.RawOK {
						res.ReverseDivergences++
					}
					step.Lost = divergenceLoss(cur.st.raw, child.raw, child.tag)
					if len(step.Lost) > 0 {
						nodes = append(nodes, bfsNode{st: child, parent: queue[head], step: step})
						w := buildWitness(len(nodes) - 1)
						res.Losses = append(res.Losses, LossRecord{Length: w.Length(), Lost: w.Lost})
						if res.Witness == nil {
							res.Witness = w
							if stopAtFirst {
								res.Explored = len(visited)
								return res, nil
							}
						}
					} else {
						res.DivergentLossless++
					}
					continue // 首次分歧已发生，剪枝该分支
				}
			} else {
				child.raw.apply(t, op, RawAddr)
				child.tag.apply(t, op, Tagged)
			}
			k := child.key()
			if visited[k] {
				continue
			}
			if len(visited) >= maxStates {
				res.Truncated = true
				res.Explored = len(visited)
				return res, nil
			}
			visited[k] = true
			nodes = append(nodes, bfsNode{st: child, parent: queue[head], step: step})
			queue = append(queue, len(nodes)-1)
		}
	}
	res.Explored = len(visited)
	return res, nil
}

// FindShortestLoss 寻找两种语义首次分歧且造成事件丢失的最短交错。
// 返回的 Result.Witness 为 nil 表示脚本不存在此类交错。
func FindShortestLoss(scripts [][]Op) (*Result, error) {
	return enumerate(scripts, true, DefaultMaxStates)
}

// EnumerateAll 做完整状态枚举，核对所有首次分歧丢事件的交错，
// 用于验证 FindShortestLoss 给出的见证确实最短、丢失事件 ID 确实发生。
func EnumerateAll(scripts [][]Op, maxStates int) (*Result, error) {
	return enumerate(scripts, false, maxStates)
}

// RunSchedule 按给定交错（线程号序列）同时执行裸地址 CAS 与带代次 CAS 两种语义，
// 返回每步的轨迹，供宿主核对两种 CAS 的逐步结果。
func RunSchedule(scripts [][]Op, sched []int) ([]StepTrace, error) {
	if err := Validate(scripts); err != nil {
		return nil, err
	}
	n := len(scripts)
	raw, tag := newMachine(n), newMachine(n)
	traces := make([]StepTrace, 0, len(sched))
	diverged := false
	for i, t := range sched {
		if t < 0 || t >= n {
			return nil, fmt.Errorf("第 %d 步: 非法线程号 %d", i+1, t)
		}
		pc := raw.pc[t]
		if pc >= len(scripts[t]) {
			return nil, fmt.Errorf("第 %d 步: 线程 T%d 的脚本已执行完", i+1, t)
		}
		op := scripts[t][pc]
		raw.pc[t]++
		tag.pc[t]++
		step := StepTrace{Thread: t, Op: op}
		if op.Kind == OpCAS {
			step.IsCAS = true
			before := raw.clone()
			step.RawOK = raw.apply(t, op, RawAddr)
			step.TaggedOK = tag.apply(t, op, Tagged)
			if !diverged && step.RawOK != step.TaggedOK {
				step.Diverged = true
				diverged = true
				step.Lost = divergenceLoss(before, raw, tag)
			}
		} else {
			raw.apply(t, op, RawAddr)
			tag.apply(t, op, Tagged)
		}
		traces = append(traces, step)
	}
	return traces, nil
}
