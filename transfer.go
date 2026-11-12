package netsec

import (
	"fmt"
	"sort"
)

func (az *analyzer) transferOp(cur *frame, b BlockID, idx int, op *Op) {
	switch op.Kind {
	case OpInput:
		cur.set(op.Dst, ExternalTop())
	case OpBase:
		cur.set(op.Dst, RegionBase(op.Region))
	case OpAddConst:
		cur.set(op.Dst, cur.get(op.Src).AddConst(op.Const))
	case OpCopy:
		cur.set(op.Dst, cur.get(op.Src).Clone())
	case OpLoad:
		az.certifyLoad(cur, b, idx, op)
		// 读取得到的数据视为不可信外部值，防止把数据当指针伪造地址。
		cur.set(op.Dst, ExternalTop())
	default:
		az.addViolation(op.Site, b, idx, fmt.Sprintf("未知操作类型 %d", op.Kind))
	}
}

func (az *analyzer) transferTerm(cur *frame, b BlockID, term *Terminator) {
	switch term.Kind {
	case TermReturn:
		return
	case TermJump:
		az.edge(cur, b, term.Then)
	case TermBranch:
		src := cur.get(term.Src)
		takeIv, okTake := branchInterval(src.Scalar, term.Cmp, term.Const)
		// 成立边：收窄操作数（外部输入在此完成显式边界检查）。
		if okTake && !takeIv.IsEmpty() {
			taken := cur.clone()
			// 标量与仿射地址都按条件收窄：地址收窄的是相对来源的偏移区间，
			// 从而让循环守卫把归纳变量限制在真实回边范围内。
			taken.set(term.Src, src.Narrow(takeIv, branchSite(b, term)))
			az.edge(taken, b, term.Then)
		}
		// 不成立边：保留原值（不假设补集的精确范围，保持可靠）。
		az.edge(cur, b, term.Else)
	default:
		az.addViolation("", b, -1, fmt.Sprintf("未知终结符类型 %d", term.Kind))
	}
}

// branchInterval 计算条件成立时操作数被收窄到的区间。
// CmpNE 无法可靠表示补集区间，返回 ok=false 表示不做收窄。
func branchInterval(src Interval, cmp uint8, c int64) (iv Interval, ok bool) {
	switch cmp {
	case CmpEQ:
		if src.Contains(c) {
			return Point(c), true
		}
		return Empty(), true
	case CmpLE:
		return src.Intersect(Bounded(minInt, c)), true
	case CmpLT:
		return src.Intersect(Bounded(minInt, c-1)), true
	case CmpGE:
		return src.Intersect(Bounded(c, maxInt)), true
	case CmpGT:
		return src.Intersect(Bounded(c+1, maxInt)), true
	default: // CmpNE 及其它：不做收窄。
		return src, false
	}
}

func branchSite(b BlockID, term *Terminator) string {
	return fmt.Sprintf("branch:b%d:r%d", b, term.Src)
}

// checkInvariant 验证到达循环头的状态被宿主给定不变量容纳（保持性）。
func (az *analyzer) checkInvariant(header, from BlockID, incoming *frame) {
	lc := az.loop[header]
	lc.CheckedIn = append(lc.CheckedIn, from)
	inv := frameFromInvariant(az.prog.Invariants[header])
	if frameLeq(incoming, inv) {
		return
	}
	lc.Holds = false
	lc.Violations = append(lc.Violations,
		fmt.Sprintf("块 %d 到达循环头 %d 的状态不满足给定不变量", from, header))
	az.viol = append(az.viol, Violation{
		Site:   fmt.Sprintf("loop:%d", header),
		Block:  header,
		Op:     -1,
		Reason: fmt.Sprintf("不变量保持性失败（来自块 %d）", from),
	})
}

func (az *analyzer) buildReport() *Report {
	rep := &Report{Violations: append([]Violation(nil), az.viol...)}

	keys := make([]string, 0, len(az.certs))
	for k := range az.certs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		rep.Certificates = append(rep.Certificates, az.certs[k])
	}

	headers := make([]BlockID, 0, len(az.loop))
	for h := range az.loop {
		headers = append(headers, h)
	}
	sort.Slice(headers, func(i, j int) bool { return headers[i] < headers[j] })
	for _, h := range headers {
		lc := az.loop[h]
		if len(lc.Violations) == 0 {
			lc.Holds = true
		}
		rep.LoopChecks = append(rep.LoopChecks, *lc)
	}

	rep.OK = len(rep.Violations) == 0
	return rep
}
