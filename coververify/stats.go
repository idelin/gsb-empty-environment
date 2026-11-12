package coververify

import "sort"

// SubmitResult 是单次 [Verifier.Submit] 的裁决结果。
type SubmitResult struct {
	// UID 是按四要素内容派生的确定性回执标识。
	UID string

	// Status 是本次回执（或重复回执对应原件）的核验状态。
	Status Status

	// Duplicate 为 true 时表示四要素与既有回执完全相同，本次不重复计边。
	Duplicate bool

	// DuplicateOf 在重复回执时指向原件 UID。
	DuplicateOf string

	// Detail 记录冲突/异常等状态的人类可读说明。
	Detail string
}

// ReceiptInfo 是供宿主审计的回执快照。
type ReceiptInfo struct {
	Receipt     Receipt
	UID         string
	Status      Status
	Duplicate   bool
	DuplicateOf string

	// Rejected 仅在 Status 为 [StatusCovered] 时有意义，标明覆盖的是否为拒绝边。
	Rejected bool
	Detail   string
}

// Stats 是覆盖统计快照。任何未通过重放核验的回执都不会抬高覆盖数字。
type Stats struct {
	// Submissions 是受理的回执总数（含重复与无效回执）。
	Submissions int

	// DistinctEdges 是有效回执涉及的去重转换边数量。
	DistinctEdges int

	// CoveredEdges 是其中已覆盖的转换边数量（冲突边、断链边均不计）。
	CoveredEdges int

	// RequiredTotal 是宿主声明必须覆盖的转换边数量。
	RequiredTotal int

	// RequiredCovered 是已满足的必需转换边数量（拒绝预期也要匹配）。
	RequiredCovered int

	// MissingRequired 是尚未覆盖（或拒绝预期不符）的必需转换边，按确定性顺序排列。
	MissingRequired []TransitionKey

	// Pending、Mismatch、Conflict、Errored、Invalid、Duplicate 是各状态回执计数。
	// 重复回执只计入 Duplicate，不参与其他状态计数。
	Pending   int
	Mismatch  int
	Conflict  int
	Errored   int
	Invalid   int
	Duplicate int

	// Coverage 是覆盖率：存在必需边时为 RequiredCovered/RequiredTotal；
	// 否则为 CoveredEdges/DistinctEdges；没有任何边时为 0。
	Coverage float64
}

// Snapshot 返回全部回执的审计快照，顺序与提交顺序一致；重复回执的状态实时镜像原件。
func (v *Verifier) Snapshot() []ReceiptInfo {
	v.mu.Lock()
	defer v.mu.Unlock()

	out := make([]ReceiptInfo, 0, len(v.entries))
	for _, e := range v.entries {
		out = append(out, ReceiptInfo{
			Receipt:     e.receipt,
			UID:         e.uid,
			Status:      e.status,
			Duplicate:   e.dup,
			DuplicateOf: e.dupOf,
			Rejected:    e.reject,
			Detail:      e.detail,
		})
	}
	return out
}

// Stats 计算当前覆盖统计，结果对状态判定完全自洽。
func (v *Verifier) Stats() Stats {
	v.mu.Lock()
	defer v.mu.Unlock()

	st := Stats{Submissions: len(v.entries)}
	edgeCovered := make(map[TransitionKey]bool)
	edgeAny := make(map[TransitionKey]struct{})

	for _, e := range v.entries {
		if e.invalid {
			st.Invalid++
			continue
		}
		if e.dup {
			st.Duplicate++
			continue
		}
		edgeAny[e.edge] = struct{}{}
		switch e.status {
		case StatusCovered:
			edgeCovered[e.edge] = true
		case StatusPending:
			st.Pending++
		case StatusMismatch:
			st.Mismatch++
		case StatusConflict:
			st.Conflict++
		case StatusErrored:
			st.Errored++
		}
	}

	st.DistinctEdges = len(edgeAny)
	st.CoveredEdges = len(edgeCovered)
	st.RequiredTotal = len(v.required)

	for edge, expectReject := range v.required {
		if covered, reject, ok := v.edgeOutcome(edge); ok && covered && reject == expectReject {
			st.RequiredCovered++
			continue
		}
		st.MissingRequired = append(st.MissingRequired, edge)
	}
	sortEdges(st.MissingRequired)

	switch {
	case st.RequiredTotal > 0:
		st.Coverage = float64(st.RequiredCovered) / float64(st.RequiredTotal)
	case st.DistinctEdges > 0:
		st.Coverage = float64(st.CoveredEdges) / float64(st.DistinctEdges)
	default:
		st.Coverage = 0
	}
	return st
}

// edgeOutcome 返回某条边当前是否已覆盖以及其拒绝标记；冲突/断链边视为未覆盖。
func (v *Verifier) edgeOutcome(edge TransitionKey) (covered bool, rejected bool, ok bool) {
	found := false
	for _, e := range v.entries {
		if e.invalid || e.dup || e.edge != edge {
			continue
		}
		found = true
		if e.status == StatusCovered {
			return true, e.reject, true
		}
	}
	return false, false, found
}

func sortEdges(edges []TransitionKey) {
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].ModelVersion != edges[j].ModelVersion {
			return edges[i].ModelVersion < edges[j].ModelVersion
		}
		if edges[i].PrevState != edges[j].PrevState {
			return edges[i].PrevState < edges[j].PrevState
		}
		return edges[i].Input < edges[j].Input
	})
}
