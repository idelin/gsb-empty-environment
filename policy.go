package netsec

import (
	"fmt"
	"slices"
	"strings"
)

// Reason 标识轨迹无法获得合法证明的具体原因。
type Reason string

const (
	// ReasonUnknownLabel 事件引用了偏序中不存在的标签。
	ReasonUnknownLabel Reason = "unknown_label"
	// ReasonIncomparableFlow 事件在互不可比的标签之间流动，既非升级也非降级。
	ReasonIncomparableFlow Reason = "incomparable_flow"
	// ReasonNoTransition 目标降级没有任何已声明的审核转换边。
	ReasonNoTransition Reason = "no_transition"
	// ReasonSubjectDenied 存在候选转换，但事件主体均未获得授权。
	ReasonSubjectDenied Reason = "subject_denied"
	// ReasonEvidenceExhausted 主体已获授权，但全部候选转换都缺少可消费的前置证据。
	ReasonEvidenceExhausted Reason = "evidence_exhausted"
)

// EvidenceSpec 声明一条共享证据及其在整条轨迹中的最大消费次数。
type EvidenceSpec struct {
	ID       string
	Capacity int
}

// Transition 是一条指定的审核转换边：仅允许 Subject 把对象从 From 降级
// 到 To，且每次使用都要消费 Requires 中列出的全部前置证据各一次。
type Transition struct {
	Name     string
	Subject  string
	From     string
	To       string
	Requires []string
}

// Event 是轨迹中的一次主机操作。Subject 为操作主体，Object 为被操作
// 对象，From/To 为操作前后的安全标签。
type Event struct {
	Subject string
	Object  string
	From    string
	To      string
}

// MissingCondition 描述候选审核转换未能成立的一项缺失条件。
type MissingCondition struct {
	Reason     Reason
	Transition string
	Subject    string
	Evidence   string
	Detail     string
}

// Violation 是轨迹中首个无法取得合法证明的事件，以及全部缺失条件。
type Violation struct {
	EventIndex        int
	Event             Event
	Reason            Reason
	MissingConditions []MissingCondition
}

// Error 使 Violation 实现 error，方便策略宿主直接作为错误返回。
func (v *Violation) Error() string {
	parts := make([]string, 0, len(v.MissingConditions))
	for _, missing := range v.MissingConditions {
		parts = append(parts, missing.Detail)
	}
	return fmt.Sprintf("netsec: event %d (%s: %s -> %s) unjustified: %s",
		v.EventIndex, v.Event.Subject, v.Event.From, v.Event.To, strings.Join(parts, "; "))
}

// ProofStep 记录证明一个事件合法所用的转换；普通操作为空转换。
type ProofStep struct {
	EventIndex int
	Event      Event
	Transition string
	Consumed   []string
}

// Proof 是覆盖整条轨迹的一条合法证明路径，包括每一步的证据消费情况。
type Proof struct {
	Steps        []ProofStep
	EvidenceLeft []int
}

// Validator 持有主机策略的不可变声明，供策略宿主对事件轨迹做路径验证。
type Validator struct {
	poset       *Poset
	evidence    []EvidenceSpec
	evIndex     map[string]int
	transitions []Transition
}

// NewValidator 校验并构造策略验证器：证据容量必须为正，转换边必须是
// 偏序中的严格降级，引用的主体证据必须存在且不得重复。
func NewValidator(poset *Poset, evidence []EvidenceSpec, transitions []Transition) (*Validator, error) {
	if poset == nil {
		return nil, fmt.Errorf("netsec: validator requires a non-nil poset")
	}
	evIndex := make(map[string]int, len(evidence))
	for i, ev := range evidence {
		if ev.ID == "" {
			return nil, fmt.Errorf("netsec: evidence at index %d has empty id", i)
		}
		if _, dup := evIndex[ev.ID]; dup {
			return nil, fmt.Errorf("netsec: duplicate evidence id %q", ev.ID)
		}
		if ev.Capacity < 1 {
			return nil, fmt.Errorf("netsec: evidence %q capacity must be >= 1, got %d", ev.ID, ev.Capacity)
		}
		evIndex[ev.ID] = i
	}

	seenTransition := make(map[string]bool, len(transitions))
	for i, tr := range transitions {
		if tr.Name == "" {
			return nil, fmt.Errorf("netsec: transition at index %d has empty name", i)
		}
		if seenTransition[tr.Name] {
			return nil, fmt.Errorf("netsec: duplicate transition name %q", tr.Name)
		}
		seenTransition[tr.Name] = true
		if tr.Subject == "" {
			return nil, fmt.Errorf("netsec: transition %q requires a subject", tr.Name)
		}
		fromIdx, okFrom := poset.indexOf(tr.From)
		if !okFrom {
			return nil, fmt.Errorf("netsec: transition %q references unknown from label %q", tr.Name, tr.From)
		}
		toIdx, okTo := poset.indexOf(tr.To)
		if !okTo {
			return nil, fmt.Errorf("netsec: transition %q references unknown to label %q", tr.Name, tr.To)
		}
		if poset.order(fromIdx, toIdx) != RelationGreater {
			return nil, fmt.Errorf("netsec: transition %q must be a strict downgrade (%s -> %s is not)", tr.Name, tr.From, tr.To)
		}
		if len(tr.Requires) == 0 {
			return nil, fmt.Errorf("netsec: transition %q requires at least one prerequisite evidence", tr.Name)
		}
		seenEvidence := make(map[string]bool, len(tr.Requires))
		for _, evID := range tr.Requires {
			if _, ok := evIndex[evID]; !ok {
				return nil, fmt.Errorf("netsec: transition %q references unknown evidence %q", tr.Name, evID)
			}
			if seenEvidence[evID] {
				return nil, fmt.Errorf("netsec: transition %q lists evidence %q more than once", tr.Name, evID)
			}
			seenEvidence[evID] = true
		}
	}

	return &Validator{
		poset:       poset,
		evidence:    append([]EvidenceSpec(nil), evidence...),
		evIndex:     evIndex,
		transitions: append([]Transition(nil), transitions...),
	}, nil
}

// stateNode 是小状态枚举中的一个节点：剩余证据向量及其前驱，用于回溯证明。
type stateNode struct {
	remaining []int
	parent    *stateNode
	step      ProofStep
}

func stateKey(remaining []int) string {
	return strings.Join(intsToStrings(remaining), ",")
}

func intsToStrings(values []int) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = fmt.Sprintf("%d", v)
	}
	return out
}

// Verify 沿事件轨迹推进小状态枚举，返回首个无合法证明的降级及其缺失条件。
// 若整条轨迹均可证明，返回 nil 与覆盖全部事件的一条证明路径。
//
// 普通操作（标签不变或升级）不消费证据；严格降级只能通过主体匹配、
// 且前置证据仍有剩余次数的审核转换边完成。任一可达状态走通即视为可证明，
// 所有可达状态均失败时报告首个违规事件。
func (v *Validator) Verify(events []Event) (*Violation, *Proof) {
	nodes := []*stateNode{{remaining: v.initialRemaining()}}

	for eventIdx, event := range events {
		fromIdx, okFrom := v.poset.indexOf(event.From)
		if !okFrom {
			return v.violation(eventIdx, event, ReasonUnknownLabel, []MissingCondition{{
				Reason: ReasonUnknownLabel,
				Detail: fmt.Sprintf("from label %q is not part of the label poset", event.From),
			}}), nil
		}
		toIdx, okTo := v.poset.indexOf(event.To)
		if !okTo {
			return v.violation(eventIdx, event, ReasonUnknownLabel, []MissingCondition{{
				Reason: ReasonUnknownLabel,
				Detail: fmt.Sprintf("to label %q is not part of the label poset", event.To),
			}}), nil
		}

		switch v.poset.order(fromIdx, toIdx) {
		case RelationEqual, RelationLess:
			// 不降级（标签不变或升级）：普通操作直接放行，证据状态不变。
			next := make([]*stateNode, 0, len(nodes))
			for _, node := range nodes {
				next = append(next, &stateNode{
					remaining: node.remaining,
					parent:    node,
					step:      ProofStep{EventIndex: eventIdx, Event: event},
				})
			}
			nodes = next
			continue
		case RelationIncomparable:
			return v.violation(eventIdx, event, ReasonIncomparableFlow, []MissingCondition{{
				Reason: ReasonIncomparableFlow,
				Detail: fmt.Sprintf("labels %q and %q are incomparable; flow is neither promotion nor downgrade", event.From, event.To),
			}}), nil
		}

		// 严格降级：枚举每个可达状态上所有可成立的审核转换。
		nextByKey := make(map[string]*stateNode)
		var missing []MissingCondition
		for _, node := range nodes {
			for _, tr := range v.matchingTransitions(event) {
				if tr.Subject != event.Subject {
					missing = append(missing, MissingCondition{
						Reason:     ReasonSubjectDenied,
						Transition: tr.Name,
						Subject:    event.Subject,
						Detail:     fmt.Sprintf("subject %q is not authorized by transition %q", event.Subject, tr.Name),
					})
					continue
				}
				blockedBy := v.blockedEvidence(node.remaining, tr)
				if blockedBy != "" {
					missing = append(missing, MissingCondition{
						Reason:     ReasonEvidenceExhausted,
						Transition: tr.Name,
						Subject:    event.Subject,
						Evidence:   blockedBy,
						Detail:     fmt.Sprintf("transition %q cannot consume exhausted evidence %q", tr.Name, blockedBy),
					})
					continue
				}

				nextRemaining := append([]int(nil), node.remaining...)
				for _, evID := range tr.Requires {
					nextRemaining[v.evIndex[evID]]--
				}
				key := stateKey(nextRemaining)
				if _, seen := nextByKey[key]; !seen {
					nextByKey[key] = &stateNode{
						remaining: nextRemaining,
						parent:    node,
						step: ProofStep{
							EventIndex: eventIdx,
							Event:      event,
							Transition: tr.Name,
							Consumed:   append([]string(nil), tr.Requires...),
						},
					}
				}
			}
		}

		if len(nextByKey) == 0 {
			missing = dedupeMissing(missing)
			reason := ReasonNoTransition
			if len(missing) > 0 {
				reason = missing[0].Reason
			} else {
				missing = []MissingCondition{{
					Reason: ReasonNoTransition,
					Detail: fmt.Sprintf("no declared review transition covers downgrade %q -> %q", event.From, event.To),
				}}
			}
			return v.violation(eventIdx, event, reason, missing), nil
		}

		nodes = nodes[:0]
		for _, next := range nextByKey {
			nodes = append(nodes, next)
		}
	}

	// 所有状态均已覆盖全部事件，选剩余证据总量最少的终态，保证证明确定。
	leaf := nodes[0]
	for _, node := range nodes[1:] {
		if sumInts(node.remaining) < sumInts(leaf.remaining) {
			leaf = node
		}
	}
	steps := make([]ProofStep, 0, len(events))
	for node := leaf; node.parent != nil; node = node.parent {
		steps = append(steps, node.step)
	}
	slices.Reverse(steps)
	return nil, &Proof{Steps: steps, EvidenceLeft: leaf.remaining}
}

func sumInts(values []int) int {
	total := 0
	for _, v := range values {
		total += v
	}
	return total
}

func (v *Validator) initialRemaining() []int {
	remaining := make([]int, len(v.evidence))
	for i, ev := range v.evidence {
		remaining[i] = ev.Capacity
	}
	return remaining
}

func (v *Validator) matchingTransitions(event Event) []Transition {
	var matches []Transition
	for _, tr := range v.transitions {
		if tr.From == event.From && tr.To == event.To {
			matches = append(matches, tr)
		}
	}
	return matches
}

// blockedEvidence 返回首个已耗尽的前置证据 ID；全部可消费时返回空串。
func (v *Validator) blockedEvidence(remaining []int, tr Transition) string {
	for _, evID := range tr.Requires {
		if remaining[v.evIndex[evID]] <= 0 {
			return evID
		}
	}
	return ""
}

func (v *Validator) violation(eventIdx int, event Event, reason Reason, missing []MissingCondition) *Violation {
	return &Violation{
		EventIndex:        eventIdx,
		Event:             event,
		Reason:            reason,
		MissingConditions: missing,
	}
}

func dedupeMissing(in []MissingCondition) []MissingCondition {
	seen := make(map[string]bool, len(in))
	out := make([]MissingCondition, 0, len(in))
	for _, m := range in {
		key := string(m.Reason) + "|" + m.Transition + "|" + m.Evidence
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, m)
	}
	slices.SortFunc(out, func(a, b MissingCondition) int {
		if a.Reason != b.Reason {
			if a.Reason < b.Reason {
				return -1
			}
			return 1
		}
		if a.Transition != b.Transition {
			if a.Transition < b.Transition {
				return -1
			}
			return 1
		}
		if a.Evidence < b.Evidence {
			return -1
		}
		if a.Evidence > b.Evidence {
			return 1
		}
		return 0
	})
	return out
}
