package netsec

import "fmt"

// Status 是分析结论状态。
type Status int

const (
	// StatusNone 表示已无遗漏枚举全部故障，未发现逃逸故障。
	StatusNone Status = iota
	// StatusFound 表示已无遗漏枚举全部故障，且存在逃逸故障。
	StatusFound
	// StatusUnknown 表示预算耗尽，结论未知（返回部分结果）。
	StatusUnknown
	// StatusError 表示输入非法或内部不一致。
	StatusError
)

func (s Status) String() string {
	switch s {
	case StatusNone:
		return "none"
	case StatusFound:
		return "found"
	case StatusUnknown:
		return "unknown"
	case StatusError:
		return "error"
	}
	return fmt.Sprintf("Status(%d)", int(s))
}

// Escape 描述一个逃逸故障及其证据。
type Escape struct {
	Fault Fault  // 逃逸的固定故障
	Input uint32 // 逃逸输入（使安全位 0->1 的最小输入向量）
	// 以下两个字段由独立标量回放产生，给出逃逸输入下的逐节点状态。
	NormalState []byte // 正常电路逐节点取值
	FaultyState []byte // 注入故障后逐节点取值
}

// Report 是逃逸分析结果。
type Report struct {
	Status      Status
	Escapes     []Escape // 全部逃逸故障（Status 为 Found/Unknown 时非空）
	FaultsTotal int      // 枚举空间中的故障总数
	FaultsDone  int      // 实际完成评估的故障数
	Err         error
}

// Analyze 枚举所有内部节点的固定 0/固定 1 故障，找出满足以下条件的逃逸故障：
// 公开测试向量集合中没有任何向量能观测到该故障（任一主输出取值不同即可观测），
// 且存在某个输入使安全输出位由拒绝 0 变为放行 1。
//
// 采用位并行仿真：每个节点的全部 2^NumInputs 个取值压缩为机器字并行求值，
// 因此默认即可穷尽整个输入空间。每个逃逸故障的证据（逃逸输入、逐节点状态）
// 由独立的标量回放产生并与位并行结果交叉校验。
//
// budget 为可评估的故障数上限；budget < 0 表示不限。预算耗尽时返回
// StatusUnknown 及已发现的部分逃逸故障；只有完整枚举后才会报告 StatusNone。
func Analyze(c *Circuit, tests []uint32, budget int64) Report {
	if err := c.Validate(); err != nil {
		return Report{Status: StatusError, Err: err}
	}
	space := c.space()
	for _, t := range tests {
		if t >= uint32(space) {
			return Report{Status: StatusError, Err: fmt.Errorf("netsec: test vector %d out of range [0,%d)", t, space)}
		}
	}

	words := wordsOf(space)
	valid := validMask(space)
	pats := inputPatterns(c.NumInputs, space)

	normal := newBitsets(len(c.Nodes), words)
	simulate(c, pats, valid, normal, 0)

	outputs := c.outputNodes()

	testMask := make(bitset, words)
	for _, t := range tests {
		testMask[t/64] |= 1 << (uint(t) % 64)
	}
	// 预计算公开测试下正常电路的标量状态，用于独立交叉校验。
	normalAtTest := make(map[uint32][]byte, len(tests))
	for _, t := range tests {
		normalAtTest[t] = evalScalar(c, t, nil)
	}

	faultNodes := c.faultableNodes()
	rep := Report{FaultsTotal: 2 * len(faultNodes)}

	tmp := make(bitset, words)
	for _, n := range faultNodes {
		for stuck := 0; stuck <= 1; stuck++ {
			if budget >= 0 && int64(rep.FaultsDone) >= budget {
				rep.Status = StatusUnknown
				return rep
			}
			rep.FaultsDone++

			vals := cloneBitsets(normal)
			setConst(vals[n], valid, stuck)
			simulate(c, pats, valid, vals, n+1)

			// 公开测试是否已观测到该故障（任一主输出不同）。
			detected := false
			for _, o := range outputs {
				diffInto(tmp, vals[o], normal[o])
				if anyAnd(tmp, testMask) {
					detected = true
					break
				}
			}
			if detected {
				continue
			}

			// 危险条件：存在输入使安全位 正常0 -> 故障1。
			andNotInto(tmp, vals[c.Secure], normal[c.Secure])
			esc, ok := lowestBit(tmp)
			if !ok {
				continue
			}

			fault := Fault{Node: n, Stuck: stuck}
			// 独立标量回放：产生逐节点状态并交叉校验位并行结果。
			nState := evalScalar(c, uint32(esc), nil)
			fState := evalScalar(c, uint32(esc), &fault)
			if nState[c.Secure] != 0 || fState[c.Secure] != 1 {
				return Report{Status: StatusError, Err: fmt.Errorf("netsec: replay mismatch at fault %+v input %d", fault, esc)}
			}
			for _, t := range tests {
				fs := evalScalar(c, t, &fault)
				ns := normalAtTest[t]
				for _, o := range outputs {
					if fs[o] != ns[o] {
						return Report{Status: StatusError, Err: fmt.Errorf("netsec: replay mismatch at fault %+v test %d", fault, t)}
					}
				}
			}

			rep.Escapes = append(rep.Escapes, Escape{
				Fault:       fault,
				Input:       uint32(esc),
				NormalState: nState,
				FaultyState: fState,
			})
		}
	}

	if len(rep.Escapes) > 0 {
		rep.Status = StatusFound
	} else {
		rep.Status = StatusNone
	}
	return rep
}
