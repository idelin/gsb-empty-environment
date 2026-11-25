package permcompile

import (
	"fmt"
	"sort"
)

// Verdict 是运行时对一次读取请求的三态结论。
type Verdict int

const (
	VerdictAllow Verdict = iota
	VerdictDeny
	VerdictWait
)

// ReadRequest 是服务端索取的一次同对象字节区间读取。
type ReadRequest struct {
	Object ObjectID
	Gen    Generation
	Range  ByteRange
}

// WaitEvent 描述需要等待哪个已认证选择事件。
type WaitEvent struct {
	Kind   EventKind
	Gate   string
	Option string // 仅 EventDecision 使用
}

// Decision 是 Authorize 的结果。
type Decision struct {
	Verdict Verdict
	Reason  string
	Waiting []WaitEvent
}

// DataCallback 是宿主提供的实际数据读取回调；仅在允许后被调用。
// 回调返回错误时库不提交本次消费，配额保持不变。
type DataCallback func(req ReadRequest) error

// candidate 是某个世界下可解释本次读取的一片候选叶子。
type candidate struct {
	leaf      int
	node      int
	intersect ByteRange // 请求范围与叶子授权范围的交集
}

// tentativeLeaves 收集在未消歧选择门处暂定可达的候选叶子，
// 同时严格尊重并行/顺序/回退的部分先后约束。
func (r *Runtime) tentativeLeaves(w *world, req ReadRequest) []candidate {
	cp := r.cp
	var out []candidate
	var walk func(n int)
	walk = func(n int) {
		fn := cp.nodes[n]
		switch fn.kind {
		case KindLeaf:
			st := w.state[n]
			if st == stCanceled || st == stCompleted {
				return
			}
			leaf := cp.leafOf[n]
			g := cp.leaves[leaf]
			if g.object != req.Object || g.gen != req.Gen {
				return
			}
			lo := max64(req.Range.Lo, g.scope.Lo)
			hi := min64(req.Range.Hi, g.scope.Hi)
			if hi <= lo {
				return
			}
			ls := w.leaves[leaf]
			if !ls.bytes.containsRange(lo, hi) || ls.reads.empty() {
				return
			}
			// 已在本世界发生过暂定消费的 pending 叶子不再作为新候选，
			// 避免把同一读取在未决门两侧重复扣减。
			if st == stPending && r.leafConsumed(w, leaf) {
				return
			}
			{
				out = append(out, candidate{
					leaf:      leaf,
					node:      n,
					intersect: ByteRange{Lo: lo, Hi: hi},
				})
			}
		case KindParallel, KindSequence:
			if w.state[n] == stCanceled || w.state[n] == stCompleted {
				return
			}
			for _, k := range fn.kids {
				if fn.kind == KindSequence && w.state[k] == stPending {
					continue // 顺序节点的后继暂不可达，也不来自未消歧门
				}
				walk(k)
			}
		case KindChoice:
			if w.state[n] == stCanceled || w.state[n] == stCompleted {
				return
			}
			opt, settled := w.decide[n]
			if !settled {
				opt, settled = w.tentative[n]
			}
			if !settled {
				// 未消歧：两个选项都作为暂定解释保留。
				for _, k := range fn.kids {
					walk(k)
				}
				return
			}
			walk(cp.choice[n].options[opt])
		case KindFallback:
			if w.state[n] == stCanceled || w.state[n] == stCompleted {
				return
			}
			primary, fallback := fn.kids[0], fn.kids[1]
			if w.state[fallback] == stActive || w.state[fallback] == stCompleted {
				walk(fallback)
			} else {
				if w.state[primary] != stPending {
					walk(primary)
				}
			}
		}
	}
	walk(0)
	return out
}

// leafConsumed 报告叶子相对初始许可是否已发生任何消费。
func (r *Runtime) leafConsumed(w *world, leaf int) bool {
	g := r.cp.leaves[leaf]
	ls := w.leaves[leaf]
	fullBytes := newIntervalSet(g.scope.Lo, g.scope.Hi)
	fullReads := newIntervalSet(0, g.maxReads)
	return ls.bytes.key() != fullBytes.key() || ls.reads.key() != fullReads.key()
}

// coveringSubset 枚举能完整覆盖请求区间、且与各未决门相容的候选子集。
// 每个子集是“叶子序号 -> 该叶承担的字节区间集合”的一种消费方式。
func (r *Runtime) coveringSubsets(w *world, req ReadRequest, cands []candidate) []map[int][]ByteRange {
	var results []map[int][]ByteRange
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].intersect.Lo != cands[j].intersect.Lo {
			return cands[i].intersect.Lo < cands[j].intersect.Lo
		}
		return cands[i].intersect.Hi < cands[j].intersect.Hi
	})

	var search func(idx int, coveredTo uint64, chosen map[int][]ByteRange, picks map[gatePick]bool)
	search = func(idx int, coveredTo uint64, chosen map[int][]ByteRange, picks map[gatePick]bool) {
		if coveredTo >= req.Range.Hi {
			cp := make(map[int][]ByteRange, len(chosen))
			for k, v := range chosen {
				cp[k] = append([]ByteRange(nil), v...)
			}
			results = append(results, cp)
			return
		}
		for i := idx; i < len(cands); i++ {
			c := cands[i]
			// 候选起点不得越过当前前缀（否则留空洞），且须仍有未覆盖部分。
			if c.intersect.Lo > coveredTo || c.intersect.Hi <= coveredTo {
				continue
			}
			// 同一未消歧选择门内的候选必须属于同一选项。
			pick, ok := r.gateAssignment(w, c.node)
			if ok {
				conflict := false
				for p := range picks {
					if p.gate == pick.gate && p.option != pick.option {
						conflict = true
						break
					}
				}
				if conflict {
					continue
				}
			}
			nextPicks := picks
			if ok {
				nextPicks = make(map[gatePick]bool, len(picks)+1)
				for p := range picks {
					nextPicks[p] = true
				}
				nextPicks[pick] = true
			}
			seg := ByteRange{Lo: coveredTo, Hi: min64(c.intersect.Hi, req.Range.Hi)}
			chosen[c.leaf] = append(chosen[c.leaf], seg)
			search(i+1, seg.Hi, chosen, nextPicks)
			chosen[c.leaf] = chosen[c.leaf][:len(chosen[c.leaf])-1]
			if len(chosen[c.leaf]) == 0 {
				delete(chosen, c.leaf)
			}
		}
	}

	search(0, req.Range.Lo, map[int][]ByteRange{}, map[gatePick]bool{})
	return results
}

type gatePick struct{ gate, option string }

// gateAssignment 判断叶子向上是否穿过未消歧选择门；返回门名与所需选项。
func (r *Runtime) gateAssignment(w *world, leafNode int) (gatePick, bool) {
	cp := r.cp
	n := cp.parent[leafNode]
	for n != -1 {
		fn := cp.nodes[n]
		if fn.kind == KindChoice {
			_, decided := w.decide[n]
			_, tent := w.tentative[n]
			if !decided && !tent {
				for _, k := range fn.kids {
					if nodeContains(cp, k, leafNode) {
						return gatePick{gate: fn.on, option: cp.nodes[k].opt}, true
					}
				}
			}
		}
		n = cp.parent[n]
	}
	return gatePick{}, false
}

func nodeContains(cp *CompiledPlan, root, target int) bool {
	if root == target {
		return true
	}
	for _, k := range cp.nodes[root].kids {
		if nodeContains(cp, k, target) {
			return true
		}
	}
	return false
}

func max64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

// coversWholeSet 报告单次承担区间是否完整覆盖某个仍可读区间，
// 此时该读取不消耗字节库存（同范围可凭剩余次数重读）。
func (r *Runtime) coversWholeSet(set IntervalSet, br ByteRange) bool {
	for _, in := range set {
		if in.Lo == br.Lo && in.Hi == br.Hi {
			return true
		}
	}
	return false
}

func min64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

// waitEvents 推导在某世界中“若对应事件到来即可覆盖请求”的事件集合。
func (r *Runtime) waitEvents(w *world, req ReadRequest) []WaitEvent {
	cp := r.cp
	seen := map[WaitEvent]bool{}
	var out []WaitEvent
	add := func(e WaitEvent) {
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}

	var walk func(n int)
	walk = func(n int) {
		fn := cp.nodes[n]
		switch fn.kind {
		case KindLeaf:
			// 叶子已激活仍不匹配，只能拒绝而非等待。
			return
		case KindParallel, KindSequence:
			if w.state[n] == stCanceled || w.state[n] == stCompleted {
				return
			}
			for _, k := range fn.kids {
				walk(k)
			}
		case KindChoice:
			if w.state[n] == stCanceled || w.state[n] == stCompleted {
				return
			}
			_, decided := w.decide[n]
			_, tent := w.tentative[n]
			if decided || tent {
				return
			}
			for opt := range cp.choice[n].options {
				sim := w.clone()
				sim.decide[n] = opt
				r.refresh(sim)
				if len(r.tentativeLeaves(sim, req)) > 0 {
					add(WaitEvent{Kind: EventDecision, Gate: fn.on, Option: opt})
				}
			}
		case KindFallback:
			if w.state[n] == stCanceled || w.state[n] == stCompleted {
				return
			}
			sim := w.clone()
			sim.visited[n] = true
			r.refresh(sim)
			if len(r.tentativeLeaves(sim, req)) > len(r.tentativeLeaves(w, req)) {
				add(WaitEvent{Kind: EventFallback, Gate: fn.gate})
			}
		}
	}
	walk(0)
	return out
}

// Authorize 仅做判定，不扣减配额。允许意味着所有尚存世界都能合法覆盖请求。
func (r *Runtime) Authorize(req ReadRequest) Decision {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.authorizeLocked(req)
}

func (r *Runtime) authorizeLocked(req ReadRequest) Decision {
	if r.checkExpiryLocked() {
		return Decision{Verdict: VerdictDeny, Reason: "session expired"}
	}
	if req.Range.Hi <= req.Range.Lo {
		return Decision{Verdict: VerdictDeny, Reason: "empty byte range"}
	}
	if len(r.worlds) == 0 {
		return Decision{Verdict: VerdictDeny, Reason: "no viable interpretation remains"}
	}

	waitUnion := map[WaitEvent]bool{}
	var waits []WaitEvent
	addWaits := func(evs []WaitEvent) {
		for _, e := range evs {
			if !waitUnion[e] {
				waitUnion[e] = true
				waits = append(waits, e)
			}
		}
	}

	for _, w := range r.worlds {
		cands := r.tentativeLeaves(w, req)
		if len(r.coveringSubsets(w, req, cands)) > 0 {
			continue
		}
		// 该世界当前无法覆盖：检查是否只是等待某个选择事件。
		evs := r.waitEvents(w, req)
		if len(evs) == 0 {
			return Decision{Verdict: VerdictDeny, Reason: r.denyReason(w, req)}
		}
		addWaits(evs)
	}
	if len(waits) > 0 {
		return Decision{Verdict: VerdictWait, Waiting: waits}
	}
	return Decision{Verdict: VerdictAllow}
}

func (r *Runtime) denyReason(w *world, req ReadRequest) string {
	for leaf, g := range r.cp.leaves {
		if g.object != req.Object {
			continue
		}
		node := r.cp.leafNode[leaf]
		if g.gen != req.Gen {
			return fmt.Sprintf("generation mismatch for object %q", g.object)
		}
		if w.state[node] == stCanceled {
			return "matching grant branch is canceled"
		}
		if w.leaves[leaf].bytes.empty() || w.leaves[leaf].reads.empty() {
			return "matching grant quota exhausted"
		}
	}
	return "requested byte range not covered by any active grant"
}

// Commit 提交一次读取：在每个世界上保留全部尚可成立的消费方式，
// 并在每个后继世界中精确扣减所有参与叶子的字节与次数配额。
func (r *Runtime) Commit(req ReadRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.checkExpiryLocked() {
		return errExpired
	}
	d := r.authorizeLocked(req)
	if d.Verdict != VerdictAllow {
		return fmt.Errorf("permcompile: commit rejected: %s", d.Reason)
	}

	next := map[string]*world{}
	for _, w := range r.worlds {
		cands := r.tentativeLeaves(w, req)
		subsets := r.coveringSubsets(w, req, cands)
		for _, subset := range subsets {
			nw := w.clone()
			for leaf, ranges := range subset {
				node := r.cp.leafNode[leaf]
				if pick, ok := r.gateAssignment(w, node); ok {
					gate := r.cp.gateOf[pick.gate]
					nw.tentative[gate] = pick.option
				}
				ls := nw.leaves[leaf]
				for _, br := range ranges {
					// 完整读取某个仍可读区间时保留字节范围（可凭剩余次数重读）；
					// 只有部分读取才把该区间从仍可读集合中精确切分。
					if r.coversWholeSet(ls.bytes, br) {
						continue
					}
					carved, ok := ls.bytes.carve(br.Lo, br.Hi)
					if !ok {
						return fmt.Errorf("permcompile: internal carve failure")
					}
					ls.bytes = carved
				}
				// 一次读取消耗一个次数配额，取下个可用计数坐标。
				first := ls.reads[0].Lo
				carved, ok := ls.reads.carve(first, first+1)
				if !ok {
					return fmt.Errorf("permcompile: internal read-count carve failure")
				}
				ls.reads = carved
				nw.leaves[leaf] = ls
			}
			// 暂定选择在提交后仍保留为可解释状态，等待真实事件最终裁剪。
			r.refresh(nw)
			next[nw.key()] = nw
		}
	}
	if len(next) == 0 {
		return errNoWorlds
	}
	merged := make([]*world, 0, len(next))
	for _, nw := range next {
		merged = append(merged, nw)
	}
	r.worlds = merged
	r.recordCommit(req, len(merged))
	return nil
}

// Serve 授权通过后调用数据回调；回调成功才提交消费，失败则配额不变。
func (r *Runtime) Serve(req ReadRequest, cb DataCallback) (Decision, error) {
	r.mu.Lock()
	d := r.authorizeLocked(req)
	r.mu.Unlock()
	if d.Verdict != VerdictAllow {
		return d, nil
	}
	if err := cb(req); err != nil {
		return d, err
	}
	if err := r.Commit(req); err != nil {
		return d, err
	}
	return d, nil
}
