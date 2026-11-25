package identity

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
)

type node struct {
	state      *kernelState
	prev       int
	eventID    int
	terminal   bool
	successors []int
}

// Analyze 在事件偏序上执行有界显式状态分析。它不枚举拓扑排序，而以
// （已执行集合 + 每线程身份栈 + 句柄/借用登记）为抽象状态做前向不动点。
func Analyze(in Input, budget Budget) (*AnalysisResult, error) {
	if err := validate(in); err != nil {
		return nil, err
	}
	if budget.MaxStates <= 0 {
		budget.MaxStates = defaultMaxStates
	}
	if budget.MaxTransitions <= 0 {
		budget.MaxTransitions = defaultMaxTrans
	}

	index := make(map[int]int, len(in.Events))
	for i, ev := range in.Events {
		index[ev.ID] = i
	}
	preds := buildPredecessors(in, index)

	root := freshState()
	root.fp = fingerprint(root)

	nodes := []*node{{state: root, prev: -1, eventID: -1}}
	hashIndex := map[uint64]int{root.hash: 0}
	illegal := []IllegalAttempt{}
	illegalSeen := map[string]int{}
	cutoff := false
	transitions := 0
	for head := 0; head < len(nodes); head++ {
		cur := nodes[head]
		canProgress := false
		exitBlocked := false
		for i := range in.Events {
			if transitions >= budget.MaxTransitions || len(nodes) >= budget.MaxStates {
				cutoff = true
				break
			}
			ev := in.Events[i]
			if cur.state.executed&(uint64(1)<<uint(i)) != 0 {
				continue
			}
			if !ready(cur.state.executed, preds[i], index) {
				continue
			}
			transitions++
			next, reason, _, ok := step(cur.state, i, ev)
			if !ok {
				recordIllegal(&illegal, illegalSeen, ev.ID, reason)
				if ev.Kind == Exit {
					exitBlocked = true
				}
				continue
			}
			canProgress = true
			// 按完整状态指纹合并：同一（已执行集合、身份栈、句柄/借用登记）
			// 只保留一个抽象状态，但为它保留 BFS 首个（即最短）父路径。不同
			// 身份栈必然有不同指纹，因此同集合多栈不会被错误合并。
			nd, seen := hashIndex[next.hash]
			if !seen {
				if len(nodes) >= budget.MaxStates {
					cutoff = true
					break
				}
				nd = len(nodes)
				hashIndex[next.hash] = nd
				next.fp = fingerprint(next)
				nodes = append(nodes, &node{
					state:   next,
					prev:    head,
					eventID: ev.ID,
				})
			}
			nodes[head].successors = append(nodes[head].successors, nd)
		}
		if !canProgress && !exitBlocked {
			// 剩余就绪事件全部是不可合法执行的非退出事件：偏序本身含有死事件，
			// 当前前缀就是完整合法历史，其余事件只作为非法交错审计。
			cur.terminal = true
		}
		if cutoff {
			break
		}
	}

	terminalFps := map[string]struct{}{}
	for _, nd := range nodes {
		if nd.terminal {
			terminalFps[nd.state.fp] = struct{}{}
		}
	}
	hasLegalHistory := len(terminalFps) > 0

	live := map[string]struct{}{}
	if hasLegalHistory {
		live = computeLive(nodes, terminalFps)
	} else {
		for _, nd := range nodes {
			live[nd.state.fp] = struct{}{}
		}
	}

	result := &AnalysisResult{
		ModelFingerprint:    modelFingerprint(in),
		Exhaustive:          !cutoff,
		BudgetCutoff:        cutoff,
		HasLegalHistory:     hasLegalHistory,
		StatesExplored:      len(nodes),
		TransitionsTried:    transitions,
		Reads:               map[int]*ReadResult{},
		Coverage:            map[int]*CoverageCertificate{},
		IllegalAttempts:     illegal,
		input:               in,
		budget:              budget,
		liveFps:             live,
		allFps:              map[string]struct{}{},
		StatesByExecutedSet: map[uint64][]string{},
		ReadStates:          map[int][]ReadObservation{},
	}
	for _, nd := range nodes {
		result.allFps[nd.state.fp] = struct{}{}
	}
	setFps := map[uint64]map[string]struct{}{}
	for _, nd := range nodes {
		set, ok := setFps[nd.state.executed]
		if !ok {
			set = map[string]struct{}{}
			setFps[nd.state.executed] = set
		}
		set[nd.state.fp] = struct{}{}
	}
	for mask, set := range setFps {
		list := make([]string, 0, len(set))
		for fp := range set {
			list = append(list, fp)
		}
		sort.Strings(list)
		result.StatesByExecutedSet[mask] = list
	}
	readIndexByBit := map[uint64]int{}
	for i, ev := range in.Events {
		if ev.Kind == ProtectedRead {
			readIndexByBit[uint64(1)<<uint(i)] = ev.ID
		}
	}
	readSeen := map[int]map[string]struct{}{}
	for _, nd := range nodes {
		if nd.prev < 0 {
			continue
		}
		parent := nodes[nd.prev].state.executed
		justRead := nd.state.executed ^ parent
		readID, ok := readIndexByBit[justRead]
		if !ok {
			continue
		}
		seen, ok := readSeen[readID]
		if !ok {
			seen = map[string]struct{}{}
			readSeen[readID] = seen
		}
		if _, dup := seen[nd.state.fp]; dup {
			continue
		}
		seen[nd.state.fp] = struct{}{}
		result.ReadStates[readID] = append(result.ReadStates[readID], ReadObservation{
			ExecutedSet: nd.state.executed,
			Fingerprint: nd.state.fp,
		})
	}

	for i, ev := range in.Events {
		if ev.Kind != ProtectedRead {
			continue
		}
		rr := classifyRead(in, i, nodes, live, hasLegalHistory, cutoff)
		result.Reads[ev.ID] = rr
		if hasLegalHistory && !cutoff && (rr.Class == AllConform || rr.Class == AllViolate) {
			result.Coverage[ev.ID] = buildCoverage(in, i, rr.Class, nodes, live, result.ModelFingerprint)
		}
	}
	return result, nil
}

func recordIllegal(illegal *[]IllegalAttempt, seen map[string]int, eventID int, reason string) {
	if len(*illegal) >= maxIllegalSamples {
		return
	}
	key := strconv.Itoa(eventID) + "|" + reason
	if _, exists := seen[key]; exists {
		return
	}
	seen[key] = len(*illegal)
	*illegal = append(*illegal, IllegalAttempt{EventID: eventID, Reason: reason})
}

// computeLive 反向不动点：能沿合法后继到达终态的状态才是“可扩展为完整
// 合法历史”的状态。
func computeLive(nodes []*node, terminalFps map[string]struct{}) map[string]struct{} {
	live := make([]bool, len(nodes))
	changed := true
	for changed {
		changed = false
		for i := len(nodes) - 1; i >= 0; i-- {
			if live[i] {
				continue
			}
			if _, terminal := terminalFps[nodes[i].state.fp]; terminal {
				live[i] = true
				changed = true
				continue
			}
			for _, succ := range nodes[i].successors {
				if live[succ] {
					live[i] = true
					changed = true
					break
				}
			}
		}
	}
	out := map[string]struct{}{}
	for i, ok := range live {
		if ok {
			out[nodes[i].state.fp] = struct{}{}
		}
	}
	return out
}

func classifyRead(in Input, idx int, nodes []*node, live map[string]struct{}, hasLegalHistory, cutoff bool) *ReadResult {
	readID := in.Events[idx].ID
	rr := &ReadResult{EventID: readID, Class: Unreachable}
	conformNode, violateNode := -1, -1
	reachable := false
	bit := uint64(1) << uint(idx)

	for nidx, nd := range nodes {
		// 只取读取事件刚执行完产生的节点（父状态尚未执行该读取），避免把读取
		// 之后归还借用等后继状态误算成读取时的身份。
		if nd.state.executed&bit == 0 || nd.prev < 0 || nodes[nd.prev].state.executed&bit != 0 {
			continue
		}
		if hasLegalHistory {
			if _, ok := live[nd.state.fp]; !ok {
				continue
			}
		}
		reachable = true
		outcome := readOutcome(nd.state.active[int32(in.Events[idx].Thread)], in.Events[idx].Policy)
		if outcome == Conform && conformNode < 0 {
			conformNode = nidx
			rr.ReachableConform = true
		}
		if outcome == Violation && violateNode < 0 {
			violateNode = nidx
			rr.ReachableViolate = true
		}
	}

	if !reachable {
		if !hasLegalHistory {
			rr.Class = Unknown
			rr.Reason = "no complete legal history exists"
		}
		return rr
	}
	if conformNode >= 0 {
		rr.ConformPrefix = reconstructPrefix(nodes, conformNode)
	}
	if violateNode >= 0 {
		rr.ViolatePrefix = reconstructPrefix(nodes, violateNode)
	}

	switch {
	case rr.ReachableConform && rr.ReachableViolate:
		rr.Class = Mixed
	case rr.ReachableConform:
		rr.Class = AllConform
		if cutoff {
			rr.Class = Unknown
			rr.Reason = "work budget exhausted before exhaustiveness was established"
		}
	case rr.ReachableViolate:
		rr.Class = AllViolate
		if cutoff {
			rr.Class = Unknown
			rr.Reason = "work budget exhausted before exhaustiveness was established"
		}
	default:
		rr.Class = Unreachable
	}
	if !hasLegalHistory {
		rr.Class = Unknown
		if rr.Reason == "" {
			rr.Reason = "no complete legal history exists; only partial legal prefixes are available"
		}
	}
	return rr
}

// reconstructPrefix 沿 BFS 父指针回溯，得到的就是该事实的最短合法前缀。
func reconstructPrefix(nodes []*node, nidx int) []int {
	var rev []int
	for nidx >= 0 && nodes[nidx].eventID >= 0 {
		rev = append(rev, nodes[nidx].eventID)
		nidx = nodes[nidx].prev
	}
	out := make([]int, len(rev))
	for i, id := range rev {
		out[len(rev)-1-i] = id
	}
	return out
}

func buildCoverage(in Input, idx int, class Class, nodes []*node, live map[string]struct{}, modelFP string) *CoverageCertificate {
	readID := in.Events[idx].ID
	outcome := Conform
	if class == AllViolate {
		outcome = Violation
	}
	bit := uint64(1) << uint(idx)
	fps := map[string]struct{}{}
	for _, nd := range nodes {
		if nd.state.executed&bit == 0 || nd.prev < 0 || nodes[nd.prev].state.executed&bit != 0 {
			continue
		}
		if _, ok := live[nd.state.fp]; !ok {
			continue
		}
		got := readOutcome(nd.state.active[int32(in.Events[idx].Thread)], in.Events[idx].Policy)
		if got != outcome {
			continue
		}
		fps[nd.state.fp] = struct{}{}
	}
	list := make([]string, 0, len(fps))
	for fp := range fps {
		list = append(list, fp)
	}
	sort.Strings(list)
	return &CoverageCertificate{
		ModelFingerprint:  modelFP,
		ReadID:            readID,
		Outcome:           outcome,
		Exhaustive:        true,
		StateFingerprints: list,
	}
}

func (r *AnalysisResult) lookupRead(readID int) (Event, error) {
	for _, ev := range r.input.Events {
		if ev.ID == readID {
			if ev.Kind != ProtectedRead {
				return Event{}, fmt.Errorf("identity: %w: event %d", errors.New(ReasonNotProtectedRead), readID)
			}
			return ev, nil
		}
	}
	return Event{}, fmt.Errorf("identity: unknown protected read %d", readID)
}
