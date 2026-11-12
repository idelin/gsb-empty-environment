package netsec

import (
	"fmt"
	"sort"
)

// ReadFromMap 归一化查询中的读自边：未显式给出的读默认读该位置的初始写。
// 返回 读事件下标 -> 写事件下标。
func ReadFromMap(q Query) (map[int]int, error) {
	prog := q.Program
	if prog == nil {
		return nil, fmt.Errorf("netsec: Query.Program 为空")
	}
	rf := make(map[int]int)
	for _, e := range q.ReadFroms {
		r, w := e.Reader, e.Writer
		if r < 0 || r >= len(prog.Events) || w < 0 || w >= len(prog.Events) {
			return nil, fmt.Errorf("netsec: 读自边事件下标越界: %d -> %d", w, r)
		}
		re, we := prog.Events[r], prog.Events[w]
		if re.IsWrite || re.IsFence {
			return nil, fmt.Errorf("netsec: 读自边的读端 %d 不是读事件", r)
		}
		if !we.IsWrite {
			return nil, fmt.Errorf("netsec: 读自边的写端 %d 不是写事件", w)
		}
		if re.Loc != we.Loc {
			return nil, fmt.Errorf("netsec: 读自边位置不一致: %q != %q", re.Loc, we.Loc)
		}
		if _, dup := rf[r]; dup {
			return nil, fmt.Errorf("netsec: 读事件 %d 有多条读自边", r)
		}
		rf[r] = w
	}
	for id, e := range prog.Events {
		if e.Thread < 0 || e.IsWrite || e.IsFence {
			continue
		}
		if _, ok := rf[id]; !ok {
			init, ok := prog.Initial[e.Loc]
			if !ok {
				return nil, fmt.Errorf("netsec: 位置 %q 缺少初始写", e.Loc)
			}
			rf[id] = init
		}
	}
	return rf, nil
}

// FailOnRead 构造断言失败谓词：一旦 bad 中的读事件已经发生，且它经读自边
// 读到的写值等于给定的禁止值，即判定断言失败（例如读到了尚未发布的秘密）。
//
// bad 的键为读事件下标，值为该读不允许观察到的值。谓词对前缀单调：失败一旦
// 发生，更长前缀上仍为失败。
func FailOnRead(rf map[int]int, bad map[int]int) FailPredicate {
	return func(prog *Program, trace Trace, prefixLen int) bool {
		for i := 0; i < prefixLen; i++ {
			id := trace[i]
			forbidden, ok := bad[id]
			if !ok {
				continue
			}
			writer := rf[id]
			if prog.Events[writer].Value == forbidden {
				return true
			}
		}
		return false
	}
}

// constraintGraph 是任何合法全序都必须满足的严格偏序（先行发生关系）。
type constraintGraph struct {
	n     int
	edge  [][]bool
	reach [][]bool
}

func buildGraph(q Query, rf map[int]int) (*constraintGraph, error) {
	prog := q.Program
	n := len(prog.Events)
	g := &constraintGraph{n: n}
	g.edge = make([][]bool, n)
	for i := range g.edge {
		g.edge[i] = make([]bool, n)
	}

	// 允许重排关系：默认采用 Program.MayReorder，宿主可显式覆盖。
	mayReorder := make(map[Pair]bool)
	if q.MayReorder != nil {
		for _, pr := range q.MayReorder {
			if pr.First < 0 || pr.First >= n || pr.Second < 0 || pr.Second >= n {
				return nil, fmt.Errorf("netsec: 重排对下标越界: %v", pr)
			}
			mayReorder[pr] = true
		}
	}
	allowed := func(a, b int) bool {
		if q.MayReorder != nil {
			return mayReorder[Pair{a, b}]
		}
		return prog.MayReorder(a, b)
	}

	// 初始写固定先于所有真实事件。
	for _, init := range prog.Initial {
		for j, e := range prog.Events {
			if e.Thread >= 0 {
				g.edge[init][j] = true
			}
		}
	}

	// 线程内程序序：除显式允许重排的相邻事件对外，其余都强制保持顺序。
	for t := 0; t < prog.ThreadCount; t++ {
		lo, hi := prog.ThreadEvents(t)
		for a := lo; a+1 < hi; a++ {
			if !allowed(a, a+1) {
				g.edge[a][a+1] = true
			}
		}
	}

	// 读自边强制写先于读；发布/获取语义再叠加同步边。
	for reader, writer := range rf {
		g.edge[writer][reader] = true
	}

	// 发布/获取同步边只取决于同位置的发布写与获取读，与读实际读到哪个写
	// 无关：即使获取读读的是旧值，同位置更晚的发布写在全序中仍必须先于该
	// 获取读（获取读不能越过发布写）。
	for ri, re := range prog.Events {
		if re.Thread < 0 || re.IsWrite || re.IsFence || !re.Order.isAcquireClass() {
			continue
		}
		for wi, we := range prog.Events {
			if we.Thread < 0 || !we.IsWrite || we.Loc != re.Loc || !we.Order.isReleaseClass() {
				continue
			}
			g.addSynchronization(prog, wi, ri)
		}
	}

	// 屏障-屏障同步（C11 风格围栏规则）：若发布屏障之后有写 w，获取屏障之前
	// 有读 r，且 r 的读自边指向 w（r 读到 w 发布的值），则两个屏障之间建立
	// 同步边。屏障再经线程内程序序把两侧全部访问纳入先行发生关系。
	for writer, w := range prog.Events {
		if w.Thread < 0 || !w.IsWrite {
			continue
		}
		for reader := range rf {
			if rf[reader] != writer {
				continue
			}
			r := prog.Events[reader]
			if r.Thread < 0 {
				continue
			}
			wlo, _ := prog.ThreadEvents(w.Thread)
			releaseFence := -1
			for f := wlo; f < writer; f++ {
				if fe := prog.Events[f]; fe.IsFence && fe.Order.isReleaseClass() {
					releaseFence = f
				}
			}
			rlo, _ := prog.ThreadEvents(r.Thread)
			acquireFence := -1
			for f := rlo; f < reader; f++ {
				if fe := prog.Events[f]; fe.IsFence && fe.Order.isAcquireClass() {
					acquireFence = f
					break
				}
			}
			if releaseFence >= 0 && acquireFence >= 0 {
				g.edge[releaseFence][acquireFence] = true
			}
		}
	}

	// Warshall 求传递闭包，得到完整先行发生关系，并检查环。
	reach := make([][]bool, n)
	for i := range reach {
		reach[i] = append([]bool(nil), g.edge[i]...)
	}
	for k := 0; k < n; k++ {
		for i := 0; i < n; i++ {
			if !reach[i][k] {
				continue
			}
			row, krow := reach[i], reach[k]
			for j := 0; j < n; j++ {
				if krow[j] {
					row[j] = true
				}
			}
		}
	}
	for i := 0; i < n; i++ {
		if reach[i][i] {
			return nil, ErrInfeasible
		}
	}
	g.reach = reach
	return g, nil
}

// addSynchronization 根据写事件与读事件的内存序添加发布/获取同步边。
//
// 覆盖四种配对：发布写-获取读、发布写-获取屏障、发布屏障-获取读、
// 发布屏障-获取屏障。屏障经线程内程序序把作用域传递给同线程其余事件。
func (g *constraintGraph) addSynchronization(prog *Program, writer, reader int) {
	w, r := prog.Events[writer], prog.Events[reader]

	// 发布写 - 获取读（含 acqrel/seqcst）。
	if w.Order.isReleaseClass() && r.Order.isAcquireClass() {
		g.edge[writer][reader] = true
	}

	if w.Thread < 0 || r.Thread < 0 {
		return
	}

	// 写之前最近的发布屏障；读之后最近的获取屏障。
	wlo, _ := prog.ThreadEvents(w.Thread)
	releaseFence := -1
	for f := wlo; f < writer; f++ {
		if fe := prog.Events[f]; fe.IsFence && fe.Order.isReleaseClass() {
			releaseFence = f
		}
	}
	_, rhi := prog.ThreadEvents(r.Thread)
	acquireFence := -1
	for f := reader + 1; f < rhi; f++ {
		if fe := prog.Events[f]; fe.IsFence && fe.Order.isAcquireClass() {
			acquireFence = f
			break
		}
	}

	// 发布写 - 获取屏障，发布屏障 - 获取读，发布屏障 - 获取屏障。
	if r.Order.isAcquireClass() && releaseFence >= 0 {
		g.edge[releaseFence][reader] = true
	}
	if w.Order.isReleaseClass() && acquireFence >= 0 {
		g.edge[writer][acquireFence] = true
	}
	if releaseFence >= 0 && acquireFence >= 0 {
		g.edge[releaseFence][acquireFence] = true
	}
}

// validate 做一次输入完整性检查，供主枚举器与参考实现共用。
func validate(q Query) (*Program, map[int]int, *constraintGraph, error) {
	rf, err := ReadFromMap(q)
	if err != nil {
		return nil, nil, nil, err
	}
	g, err := buildGraph(q, rf)
	if err != nil {
		return nil, nil, nil, err
	}
	return q.Program, rf, g, nil
}

// searchState 持有全序枚举过程中的动态状态。
type searchState struct {
	prog      *Program
	g         *constraintGraph
	rf        map[int]int
	real      []int
	placed    []bool
	indegree  []int
	lastWrite map[string]int
	trace     []int
}

func newSearchState(prog *Program, g *constraintGraph, rf map[int]int) *searchState {
	var real []int
	for id, e := range prog.Events {
		if e.Thread >= 0 {
			real = append(real, id)
		}
	}
	indegree := make([]int, len(prog.Events))
	for _, id := range real {
		for _, pred := range real {
			// 用完整先行发生关系（传递闭包）计算入度，保证放置时所有
			// 直接与间接前驱都已就位。
			if g.reach[pred][id] {
				indegree[id]++
			}
		}
	}
	return &searchState{
		prog:      prog,
		g:         g,
		rf:        rf,
		real:      real,
		placed:    make([]bool, len(prog.Events)),
		indegree:  indegree,
		lastWrite: cloneLastWrite(prog.Initial),
	}
}

func cloneLastWrite(in map[string]int) map[string]int {
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// readyEvents 返回当前可放置且满足读连贯性的事件，按下标排序保证确定性。
func (s *searchState) readyEvents() []int {
	var ready []int
	for _, id := range s.real {
		if s.placed[id] || s.indegree[id] != 0 {
			continue
		}
		e := s.prog.Events[id]
		if !e.IsWrite && !e.IsFence {
			// 读必须读到该位置当前最近一次写，即每位置写序（连贯性）。
			if s.lastWrite[e.Loc] != s.rf[id] {
				continue
			}
		}
		ready = append(ready, id)
	}
	sort.Ints(ready)
	return ready
}

func (s *searchState) place(id int) {
	s.placed[id] = true
	s.trace = append(s.trace, id)
	e := s.prog.Events[id]
	if e.IsWrite {
		s.lastWrite[e.Loc] = id
	}
	s.refreshIndegrees()
}

func (s *searchState) undo(id int) {
	e := s.prog.Events[id]
	s.trace = s.trace[:len(s.trace)-1]
	s.placed[id] = false
	if e.IsWrite {
		// 基于回退后仍在位的事件，恢复该位置的最近写；没有真实写时
		// 回落到初始写。
		delete(s.lastWrite, e.Loc)
		for _, prev := range s.trace {
			if pe := s.prog.Events[prev]; pe.IsWrite && pe.Loc == e.Loc {
				s.lastWrite[e.Loc] = prev
			}
		}
		if _, ok := s.lastWrite[e.Loc]; !ok {
			s.lastWrite[e.Loc] = s.prog.Initial[e.Loc]
		}
	}
	s.refreshIndegrees()
}

// refreshIndegrees 按当前已放置集合重算每个真实事件尚未满足的前驱数。
func (s *searchState) refreshIndegrees() {
	for _, id := range s.real {
		if s.placed[id] {
			s.indegree[id] = 0
			continue
		}
		count := 0
		for _, pred := range s.real {
			if !s.placed[pred] && s.g.reach[pred][id] {
				count++
			}
		}
		s.indegree[id] = count
	}
}

const defaultLimit = 10000

// Enumerate 返回满足全部约束的连贯执行（全序）。
//
// 已过滤违反先行发生关系的交错与违反每位置写序（读连贯性）的交错。
// q.Fail 不影响本函数；断言失败见证请用 ShortestWitness。
func Enumerate(q Query) (*Result, error) {
	prog, rf, g, err := validate(q)
	if err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit == 0 {
		limit = defaultLimit
	}
	state := newSearchState(prog, g, rf)
	res := &Result{}

	var dfs func() bool
	dfs = func() bool {
		if len(state.trace) == len(state.real) {
			res.Traces = append(res.Traces, append(Trace(nil), state.trace...))
			if limit > 0 && len(res.Traces) >= limit {
				res.Truncated = true
				return true
			}
			return false
		}
		for _, id := range state.readyEvents() {
			state.place(id)
			if dfs() {
				return true
			}
			state.undo(id)
		}
		return false
	}
	dfs()
	if len(res.Traces) == 0 {
		return nil, ErrInfeasible
	}
	return res, nil
}

// extendTrace 从当前已放置的状态出发，贪心地补全为一条完整连贯执行。
// 由于约束是偏序、读自边固定，任何连贯前缀都能被补全；找不到时返回 false。
func (s *searchState) extendTrace() (Trace, bool) {
	if len(s.trace) == len(s.real) {
		return append(Trace(nil), s.trace...), true
	}
	for _, id := range s.readyEvents() {
		s.place(id)
		if full, ok := s.extendTrace(); ok {
			return full, true
		}
		s.undo(id)
	}
	return nil, false
}

// ShortestWitness 在全部合法执行中寻找断言失败的最短见证：失败前缀长度最小，
// 长度相同时取枚举顺序中最先发现的一个。
//
// q.Fail 必须非空且对前缀单调（一旦失败，更长前缀仍失败）。返回的 Witness
// 同时给出完整连贯执行与触发失败的最短前缀；不存在合法执行时返回 ErrInfeasible，
// 合法执行都通过断言时返回 nil 错误与空见证。
func ShortestWitness(q Query) (*Witness, error) {
	if q.Fail == nil {
		return nil, fmt.Errorf("netsec: ShortestWitness 需要非空的 Fail 谓词")
	}
	prog, rf, g, err := validate(q)
	if err != nil {
		return nil, err
	}
	state := newSearchState(prog, g, rf)

	best := len(state.real) + 1
	var bestPrefix Trace

	var dfs func()
	dfs = func() {
		// 再放一个事件后，最短可能的失败前缀长度为 len(trace)+1；
		// 若已无法改进当前最优，整枝剪去。
		if len(state.trace)+1 >= best {
			return
		}
		for _, id := range state.readyEvents() {
			state.place(id)
			failAt := len(state.trace)
			if q.Fail(prog, state.trace, failAt) {
				best = failAt
				bestPrefix = append(Trace(nil), state.trace...)
				state.undo(id)
				return
			}
			dfs()
			state.undo(id)
			if best == 1 {
				return
			}
		}
	}
	dfs()

	if bestPrefix == nil {
		// 没有失败前缀：区分“所有合法执行都通过断言”与“根本不存在合法执行”。
		fresh := newSearchState(prog, g, rf)
		if _, ok := fresh.extendTrace(); !ok {
			return nil, ErrInfeasible
		}
		return nil, nil
	}

	// 用独立状态把最短失败前缀补全成一条完整连贯执行。
	fullState := newSearchState(prog, g, rf)
	for _, id := range bestPrefix {
		fullState.place(id)
	}
	full, ok := fullState.extendTrace()
	if !ok {
		return nil, ErrInfeasible
	}
	return &Witness{
		Trace:  full,
		FailAt: best,
		Prefix: bestPrefix,
	}, nil
}
