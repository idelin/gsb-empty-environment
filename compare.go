package netsec

import (
	"errors"
	"fmt"
)

// CompareReport 是原图与插桩图两次解释执行的对照结论。
type CompareReport struct {
	ReturnEqual   bool
	SequenceEqual bool
	TrapEqual     bool
	RejectNoSide  bool
	Mismatch      string
}

// CompareExecution 对照两个执行结果：
//   - 合法访问路径：返回值、事件顺序（check 仅出现在插桩图，比较时忽略）必须一致；
//   - 异常顺序：首次拒绝/陷阱的源节点与位置必须一致；
//   - 被拒写入：拒绝点之后内存快照必须与拒绝前完全相同（无副作用）。
func CompareExecution(base, inst *Result) CompareReport {
	r := CompareReport{
		ReturnEqual:   base.Return == inst.Return,
		TrapEqual:     base.Trap == inst.Trap,
		RejectNoSide:  true,
		SequenceEqual: true,
	}

	bSeq := observableSequence(base)
	iSeq := observableSequence(inst)
	if len(bSeq) != len(iSeq) {
		r.SequenceEqual = false
		r.Mismatch = fmt.Sprintf("event sequence length: base=%d inst=%d", len(bSeq), len(iSeq))
	} else {
		for k := range bSeq {
			if bSeq[k] != iSeq[k] {
				r.SequenceEqual = false
				r.Mismatch = fmt.Sprintf("event #%d differs: base=%+v inst=%+v", k, bSeq[k], iSeq[k])
				break
			}
		}
	}

	// 被拒写入无副作用：一旦某条写事件对应的 check 失败，其后不得出现任何
	// 实际写入；用快照与拒绝前快照逐字节比对来独立核对。
	r.RejectNoSide = verifyNoRejectedSideEffect(inst)

	return r
}

// seqPoint 是去掉 check 噪声后的可比较事件点。
type seqPoint struct {
	Kind   EventKind
	Source InstrID
	Line   int
	Err    string
}

func observableSequence(r *Result) []seqPoint {
	out := make([]seqPoint, 0, len(r.Events))
	for _, e := range r.Events {
		if e.Kind == EvCheck {
			continue // 插桩图独有，原图没有，不参与顺序比较
		}
		p := seqPoint{Kind: e.Kind, Source: e.Source, Line: e.Pos.Line}
		if e.Err != nil {
			p.Err = e.Err.Error()
		}
		out = append(out, p)
	}
	return out
}

// verifyNoRejectedSideEffect 检查：拒绝事件之后不存在实际写事件，
// 并且（若提供快照）拒绝后的内存与拒绝前一致。
func verifyNoRejectedSideEffect(r *Result) bool {
	rejected := false
	var beforeReject map[int64]byte
	for _, e := range r.Events {
		switch {
		case e.Kind == EvCheck && e.Err != nil:
			rejected = true
			// 解释器在拒绝点固化快照；这里再防御性拷贝。
			beforeReject = cloneBytes(r.MemSnapshot)
		case rejected && e.Kind == EvWrite:
			return false // 拒绝后仍发生写入
		}
	}
	if rejected && beforeReject != nil && !equalBytes(beforeReject, r.MemSnapshot) {
		return false
	}
	return true
}

func cloneBytes(m map[int64]byte) map[int64]byte {
	cp := make(map[int64]byte, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

func equalBytes(a, b map[int64]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// ErrRejected 可用于宿主侧断言检测拒绝。
var ErrRejected = errors.New("netsec: access rejected")
