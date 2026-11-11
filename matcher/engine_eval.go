package matcher

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
)

// windowRange 计算段窗口相对于锚点事件的左闭右开绝对区间 [lo, hi)。
func (e *Engine) windowRange(step Step, n *nodeState) (time.Time, time.Time) {
	base := n.bound[e.rule.Steps[0].Alias]
	if step.Window.BaseAlias != "" {
		base = n.bound[step.Window.BaseAlias]
	}
	return base.At.Add(step.Window.Low), base.At.Add(step.Window.High)
}

// enumerate 基于当前缓冲与水位线全量重算所有节点，分成存活与死亡两组返回。
//
// 全量重算保证乱序到达的事件可以“追溯”修正此前给出的暂定结论：只要事件
// 尚未被水位线拒绝，它就参与所有窗口的重新判定。
func (e *Engine) enumerate() (map[string]*nodeState, map[string]*nodeState) {
	live := map[string]*nodeState{}
	dead := map[string]*nodeState{}

	rootStep := e.rule.Steps[0]
	for _, be := range e.buffer {
		if be.Type != rootStep.EventType {
			continue
		}
		root := &nodeState{
			depth:    0,
			bound:    map[string]Event{rootStep.Alias: be.Event},
			lastSeq:  be.seq,
			evidence: []string{be.ID},
		}
		e.extend(root, live, dead)
	}
	// 无死因前缀可能在递归中先入 live 后又因后代全部死亡而入 dead，
	// 以 dead 为准，保证一个实例不会同时存活与定案。
	for id := range dead {
		delete(live, id)
	}
	return live, dead
}

// extend 沿规则序列深度优先扩展节点，并把每个深度的节点登记到 live/dead。
//
// 登记约定：
//   - 自身带死因（违反否定段/正段窗口关闭）的节点进 dead；
//   - 已完成且无未确认否定段的告警节点，以及仍可等待后续事件的暂定节点进 live；
//   - 仅当全部后代都带真实死因时，前缀节点才作为不可定案实例进 dead，
//     避免留下无法升级也无法解释的悬空暂定节点。
func (e *Engine) extend(n *nodeState, live, dead map[string]*nodeState) {
	id := n.matchID(e.rule.ID)
	if n.isDead() {
		dead[id] = n
		return
	}
	if n.complete {
		live[id] = n
		return
	}

	next := e.rule.Steps[n.depth+1]
	lo, hi := e.windowRange(next, n)

	if next.Negate {
		if ev := e.findNegationViolation(next, lo, hi, n.bound); ev != nil {
			child := n.clone()
			child.violatedBy = ev
			child.violatedStep = next.Alias
			child.internalTag = "neg-" + next.Alias
			e.extend(child, live, dead)
			if d, ok := dead[child.matchID(e.rule.ID)]; ok && d.isDead() {
				dead[id] = n
			}
			return
		}
		// 窗口内暂无违反事件：水位线达到段末（hi）才确认否定；在此之前
		// 节点保持暂定，后续水位线推进时全量重算会自动把它转为已确认。
		child := n.clone()
		child.depth++
		if e.watermark.IsZero() || e.watermark.Before(hi) {
			child.unconfirmed++
		}
		if child.depth == len(e.rule.Steps)-1 {
			child.complete = true
		}
		e.extend(child, live, dead)
		e.classifyPrefix(id, n, child, live, dead)
		return
	}

	candidates := e.findPositiveCandidates(next, lo, hi, n)
	if len(candidates) == 0 {
		if !e.watermark.IsZero() && !e.watermark.Before(hi) {
			// 正段窗口已在水位线处关闭且无候选：本节点过期。
			expired := n.clone()
			expired.expiredAt = hi
			expired.expiredStep = next.Alias
			expired.internalTag = "expired-" + next.Alias
			e.extend(expired, live, dead)
			dead[id] = n
			return
		}
		// 窗口尚未关闭，继续以暂定身份等待候选事件。
		live[id] = n
		return
	}

	anyLiveChild := false
	anyDeadCause := false
	for _, cand := range candidates {
		child := n.clone()
		child.depth++
		child.lastSeq = cand.seq
		child.bound[next.Alias] = cand.Event
		child.evidence = append(child.evidence, cand.ID)
		if child.depth == len(e.rule.Steps)-1 {
			child.complete = true
		}
		e.extend(child, live, dead)
		childID := child.matchID(e.rule.ID)
		if _, alive := live[childID]; alive {
			anyLiveChild = true
		} else if d := dead[childID]; d != nil && d.isDead() {
			anyDeadCause = true
		}
	}
	if anyLiveChild || !anyDeadCause {
		live[id] = n
	} else {
		dead[id] = n
	}
}

// classifyPrefix 根据唯一后代的归属决定前缀节点进 live 还是 dead。
func (e *Engine) classifyPrefix(id string, n, child *nodeState, live, dead map[string]*nodeState) {
	childID := child.matchID(e.rule.ID)
	if _, alive := live[childID]; alive {
		live[id] = n
	} else if d := dead[childID]; d != nil && d.isDead() {
		dead[id] = n
	} else {
		live[id] = n
	}
}

// findNegationViolation 在 [lo, hi) 内寻找触发否定段违反的事件，找不到返回 nil。
func (e *Engine) findNegationViolation(step Step, lo, hi time.Time, bound map[string]Event) *bufferedEvent {
	for i := range e.buffer {
		be := &e.buffer[i]
		if be.Type != step.EventType || !inWindow(be.At, lo, hi) {
			continue
		}
		if e.rule.negationKeysSatisfied(step, be.Event, bound) {
			return be
		}
	}
	return nil
}

// findPositiveCandidates 返回正段在 [lo, hi) 内可绑定的全部候选事件。
func (e *Engine) findPositiveCandidates(step Step, lo, hi time.Time, n *nodeState) []bufferedEvent {
	var out []bufferedEvent
	for i := range e.buffer {
		be := e.buffer[i]
		if be.Type != step.EventType || !inWindow(be.At, lo, hi) {
			continue
		}
		if be.seq <= n.lastSeq {
			continue
		}
		trial := cloneBound(n.bound)
		trial[step.Alias] = be.Event
		if !e.rule.keysSatisfied(trial) {
			continue
		}
		out = append(out, be)
	}
	return out
}

// refresh 重新求值并把新旧节点集合的差异转换成有序的状态变更输出。
func (e *Engine) refresh() []Output {
	live, dead := e.enumerate()
	outputs := make([]Output, 0)

	// 死亡节点的判定以“节点集合迁移”为准：上一轮在 live 中的节点本轮落入
	// dead，即代表一条暂定实例被撤销或过期；同锚点只保留证据最长的一条，
	// 避免同一实例的多个前缀重复发声。
	revokePick := map[string]*nodeState{}
	expirePick := map[string]*nodeState{}
	for _, n := range dead {
		publicID := e.rule.ID + ":" + strings.Join(n.evidence, "|")
		if _, wasLive := e.live[publicID]; !wasLive {
			continue
		}
		if !n.isDead() {
			continue
		}
		if n.violatedBy != nil {
			if cur, ok := revokePick[n.anchorID()]; !ok || len(n.evidence) > len(cur.evidence) {
				revokePick[n.anchorID()] = n
			}
		} else {
			if cur, ok := expirePick[n.anchorID()]; !ok || len(n.evidence) > len(cur.evidence) {
				expirePick[n.anchorID()] = n
			}
		}
	}

	for id, n := range dead {
		e.dead[id] = n
	}
	for _, n := range revokePick {
		outputs = append(outputs, Output{
			Kind:      OutputRevoke,
			Match:     n.snapshot(e.rule.ID),
			Reason:    fmt.Sprintf("否定段 %q 的窗口内到达匹配事件 %s，暂定结论撤销", n.violatedStep, n.violatedBy.ID),
			EventTime: n.violatedBy.At,
		})
	}
	for _, n := range expirePick {
		outputs = append(outputs, Output{
			Kind:      OutputExpire,
			Match:     n.snapshot(e.rule.ID),
			Reason:    fmt.Sprintf("正段 %q 的时间窗于 %s 关闭，仍未绑定事件", n.expiredStep, formatTime(n.expiredAt)),
			EventTime: n.expiredAt,
		})
	}

	// 本轮新出现或从暂定升级为告警的存活节点。
	for id, n := range live {
		if _, buried := e.dead[id]; buried {
			continue
		}
		old, existed := e.live[id]
		nAlerted := n.complete && n.unconfirmed == 0
		oldAlerted := existed && old.complete && old.unconfirmed == 0
		switch {
		case nAlerted && !oldAlerted:
			outputs = append(outputs, Output{
				Kind:      OutputAlert,
				Match:     n.snapshot(e.rule.ID),
				EventTime: n.bound[e.lastPositiveAlias()].At,
			})
		case !existed && len(n.evidence) == 1 && !n.complete:
			outputs = append(outputs, Output{
				Kind:      OutputTentative,
				Match:     n.snapshot(e.rule.ID),
				EventTime: n.bound[e.rule.Steps[0].Alias].At,
			})
		}
	}

	e.live = live
	sortOutputs(outputs)
	return outputs
}

// sortOutputs 让同一次求值的输出顺序确定，方便批处理与流式结果逐项比对。
func sortOutputs(out []Output) {
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Match.MatchID < out[j].Match.MatchID
	})
}

// lastPositiveAlias 返回规则最后一个正段的别名，用于定位定案时刻的事件。
func (e *Engine) lastPositiveAlias() string {
	for i := len(e.rule.Steps) - 1; i >= 0; i-- {
		if !e.rule.Steps[i].Negate {
			return e.rule.Steps[i].Alias
		}
	}
	return e.rule.Steps[0].Alias
}

// findDeathCause 在死亡集合中查找与 n 同锚点、同证据链且真正携带死因
// （内部标签节点）的节点。全量重算时前缀节点本身不带死因，死因记录在
// 带 internalTag 的兄弟节点上。
func (e *Engine) findDeathCause(n *nodeState) *nodeState {
	if n.isDead() {
		return n
	}
	publicID := e.rule.ID + ":" + strings.Join(n.evidence, "|")
	for id, d := range e.dead {
		if id == publicID || !d.isDead() {
			continue
		}
		if d.anchorID() == n.anchorID() && reflect.DeepEqual(d.evidence, n.evidence) {
			return d
		}
	}
	return nil
}
