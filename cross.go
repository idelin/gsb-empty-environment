package netsec

import "fmt"

// CrossCheckResult 是静态证书与有限具体执行交叉核对的结果。
type CrossCheckResult struct {
	OK         bool
	Cases      int
	Checked    int // 实际发生的读取事件数
	Mismatches []Mismatch
	// ConfirmedOutOfBounds 是静态已拒绝且具体执行确实越界的站点。
	ConfirmedOutOfBounds []string
	// Conservative 是静态保守拒绝、但在这些有限输入下未实际越界的站点。
	Conservative []string
	Errors       []string
}

// Mismatch 描述静态结论与具体执行不一致的一处。
type Mismatch struct {
	Site     string
	Block    BlockID
	Op       int
	Static   string
	Concrete string
}

// EnumerateInputs 按声明有限域枚举全部输入组合（笛卡尔积）。
func EnumerateInputs(prog *Program) []map[InputID]int64 {
	ids := make([]InputID, 0, len(prog.Inputs))
	for id := range prog.Inputs {
		ids = append(ids, InputID(id))
	}
	combos := []map[InputID]int64{{}}
	for _, id := range ids {
		var next []map[InputID]int64
		for _, base := range combos {
			for _, v := range prog.Inputs[id].Values {
				combo := make(map[InputID]int64, len(base)+1)
				for k, val := range base {
					combo[k] = val
				}
				combo[id] = v
				next = append(next, combo)
			}
		}
		combos = next
	}
	return combos
}

// CrossCheck 用全部有限输入组合运行解释器，并把每次具体读取与静态证书核对。
//
// 核对是双向的：
//   - 静态已认证（声称在界内）的站点，具体执行必须在界内；
//   - 静态判定违例（无法证明在界内）的站点，若具体执行越界则与保守结论一致，
//     若始终在界内则记为「精度未触发」而非错误。
func CrossCheck(prog *Program, rep *Report) (*CrossCheckResult, error) {
	if rep == nil {
		return nil, fmt.Errorf("crosscheck: 报告为空")
	}
	certified := make(map[string]Certificate, len(rep.Certificates))
	for _, c := range rep.Certificates {
		certified[certKey(c.Block, c.Op)] = c
	}
	violated := make(map[string]Violation, len(rep.Violations))
	for _, v := range rep.Violations {
		if v.Op >= 0 {
			violated[certKey(v.Block, v.Op)] = v
		}
	}

	out := &CrossCheckResult{OK: true}
	conservativeSeen := make(map[string]bool)
	confirmedSeen := make(map[string]bool)
	for _, combo := range EnumerateInputs(prog) {
		out.Cases++
		run, err := Run(prog, combo)
		if err != nil && run == nil {
			out.OK = false
			out.Errors = append(out.Errors, err.Error())
			continue
		}
		for _, ev := range run.Loads {
			out.Checked++
			key := certKey(ev.Block, ev.Op)
			if cert, ok := certified[key]; ok {
				if !ev.InBounds {
					out.OK = false
					out.Mismatches = append(out.Mismatches, Mismatch{
						Site: ev.Site, Block: ev.Block, Op: ev.Op,
						Static:   fmt.Sprintf("证书声称在界内 [%d,%d]", cert.AccessLo, cert.AccessHi),
						Concrete: fmt.Sprintf("具体读取越界 @%d 大小 %d", ev.Address, ev.Size),
					})
					continue
				}
				if !cert.OriginRanges[ev.Region].Contains(ev.Address) {
					out.OK = false
					out.Mismatches = append(out.Mismatches, Mismatch{
						Site: ev.Site, Block: ev.Block, Op: ev.Op,
						Static:   "证书区间未覆盖具体地址",
						Concrete: fmt.Sprintf("具体地址 %d 区域 %d", ev.Address, ev.Region),
					})
				}
				continue
			}
			if _, ok := violated[key]; ok {
				// 静态保守拒绝：具体越界则方向一致（确认越界）；
				// 始终在界内则记为精度未触发，均不算作不一致。
				if ev.InBounds {
					if !conservativeSeen[key] {
						conservativeSeen[key] = true
						out.Conservative = append(out.Conservative, ev.Site)
					}
				} else if !confirmedSeen[key] {
					confirmedSeen[key] = true
					out.ConfirmedOutOfBounds = append(out.ConfirmedOutOfBounds, ev.Site)
				}
				continue
			}
			out.OK = false
			out.Mismatches = append(out.Mismatches, Mismatch{
				Site:     ev.Site,
				Block:    ev.Block,
				Op:       ev.Op,
				Static:   "该读取既无证书也无违例",
				Concrete: fmt.Sprintf("具体 InBounds=%v", ev.InBounds),
			})
		}
		if err != nil && run != nil {
			// 解释器因越界返回错误，但其读取事件已在上文核对；其余运行错误登记。
			if len(run.Loads) == 0 {
				out.Errors = append(out.Errors, err.Error())
			}
		}
	}
	return out, nil
}

// Validate 是宿主的一站式入口：先做静态分析，再用有限输入解释器交叉核对。
func Validate(prog *Program) (*Report, *CrossCheckResult, error) {
	rep, err := Analyze(prog)
	if err != nil {
		return nil, nil, err
	}
	cross, err := CrossCheck(prog, rep)
	if err != nil {
		return rep, nil, err
	}
	return rep, cross, nil
}
