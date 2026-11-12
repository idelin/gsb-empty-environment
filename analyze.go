package netsec

import "fmt"

// maxAnalysisIterations 是工作列表迭代上限，超过即判定为不收敛（缺不变量）。
const maxAnalysisIterations = 1_000_000

// analyzer 持有工作列表分析的全部状态。
type analyzer struct {
	prog *Program

	in      map[BlockID]*frame // 每个块当前累积的入口框架
	queued  map[BlockID]bool
	work    []BlockID
	certs   map[string]Certificate
	viol    []Violation
	loop    map[BlockID]*LoopCheck
	headers map[BlockID]bool
	iters   int
}

// Analyze 对程序做仿射地址分析并产出每条读取的范围证书。
func Analyze(prog *Program) (*Report, error) {
	if err := ValidateProgram(prog); err != nil {
		return nil, err
	}
	az := &analyzer{
		prog:    prog,
		in:      make(map[BlockID]*frame),
		queued:  make(map[BlockID]bool),
		certs:   make(map[string]Certificate),
		loop:    make(map[BlockID]*LoopCheck),
		headers: make(map[BlockID]bool),
	}
	for h := range prog.Invariants {
		az.headers[h] = true
		az.loop[h] = &LoopCheck{Header: h}
	}

	// 入口块以空框架启动；循环头以宿主给定不变量作为归纳假设启动。
	az.in[prog.Entry] = newFrame()
	az.enqueue(prog.Entry)
	for h := range prog.Invariants {
		az.in[h] = frameFromInvariant(prog.Invariants[h])
		az.enqueue(h)
	}

	for len(az.work) > 0 {
		b := az.dequeue()
		az.iters++
		if az.iters > maxAnalysisIterations {
			return nil, fmt.Errorf(
				"analyze: 工作列表迭代超过上限 %d，可能存在未提供不变量的循环",
				maxAnalysisIterations)
		}
		az.processBlock(b)
	}

	return az.buildReport(), nil
}

func (az *analyzer) enqueue(b BlockID) {
	if !az.queued[b] {
		az.queued[b] = true
		az.work = append(az.work, b)
	}
}

func (az *analyzer) dequeue() BlockID {
	b := az.work[0]
	az.work = az.work[1:]
	az.queued[b] = false
	return b
}

func (az *analyzer) processBlock(b BlockID) {
	st := az.in[b]
	if st == nil || st.isBottom() {
		return
	}
	blk := az.prog.Blocks[b]
	cur := st.clone()

	for i := range blk.Ops {
		az.transferOp(cur, b, i, &blk.Ops[i])
	}
	az.transferTerm(cur, b, &blk.Term)
}

func (az *analyzer) addViolation(site string, b BlockID, idx int, reason string) {
	az.viol = append(az.viol, Violation{Site: site, Block: b, Op: idx, Reason: reason})
}

// edge 把出口框架送到目标块；循环头只做不变量包含性核对，不扩大其状态。
func (az *analyzer) edge(out *frame, from, to BlockID) {
	if out == nil || out.isBottom() {
		return
	}
	if int(to) >= len(az.prog.Blocks) {
		az.addViolation("", from, -1, fmt.Sprintf("控制流跳转到不存在的块 %d", to))
		return
	}
	if az.headers[to] {
		az.checkInvariant(to, from, out)
		return
	}
	if existing := az.in[to]; existing == nil {
		az.in[to] = out.clone()
		az.enqueue(to)
	} else {
		merged := existing.clone()
		merged.joinInto(out)
		// 只有合流确实放宽了入口状态才需要重新分析，保证工作列表收敛。
		if !frameLeq(merged, existing) {
			az.in[to] = merged
			az.enqueue(to)
		}
	}
}
