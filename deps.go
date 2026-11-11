package netsec

import "sort"

// buildDependencies 为子图中仍处于等待的每个锚点生成一条依赖边。
// 依赖边说明该等待卡在什么资源/消息上，以及哪些真实事件本可解除它。
func (en *engine) buildDependencies(mask uint32, pending []int) []Dependency {
	deps := make([]Dependency, 0, len(pending))
	selected := func(idx int) bool { return mask&(1<<uint(idx)) != 0 }

	for _, idx := range pending {
		e := en.events[idx]
		dep := Dependency{
			WaitIndex: idx,
			WaitActor: e.Actor,
			Resources: append([]string(nil), e.Resources...),
		}

		switch e.Kind {
		case KindReceive:
			mid := en.msgRef[idx]
			dep.Kind = "message"
			dep.BlockedOnRef = e.Ref
			if sender, ok := en.msgSender[mid]; ok {
				dep.Note = "receive waits for message; its real send is event #" + itoa(sender)
				dep.BlockedBy = append(dep.BlockedBy, sender)
			} else {
				dep.Note = "receive waits for message " + e.Ref + " which is never sent in the trace"
			}
		case KindWait:
			dep.Kind = "resource"
			blockers := map[int]bool{}
			for _, rid := range en.waitResources[idx] {
				for _, producer := range en.resProducers[rid] {
					if selected(producer) {
						blockers[producer] = true
					}
				}
			}
			for b := range blockers {
				dep.BlockedBy = append(dep.BlockedBy, b)
			}
			sort.Ints(dep.BlockedBy)
			if len(dep.BlockedBy) == 0 {
				dep.Note = "none of the candidate resources are ever produced in this subgraph"
			} else {
				dep.Note = "candidate resources are only reachable through blocked producers"
			}
		}
		deps = append(deps, dep)
	}
	return deps
}

// buildRing 在完整轨迹的 pending 等待之间寻找依赖闭环。
//
// 边的定义遵循“解除一个等待需要什么真实事件”：
//   - 资源等待：沿候选资源找到会产出该资源的 Produce/Receive；
//   - 消息接收：找到该消息真实的 Send。
//
// 若该前置事件属于另一个已卡住的参与方（它自己也停在某个 pending 等待上），
// 就得到一条 pending 等待到 pending 等待的依赖边；沿边回到起点即等待环。
// 全程只引用真实存在的事件，不补造任何消息。
func (en *engine) buildRing(pending []int, skipped []int) ([]int, bool) {
	// 环上的节点包含两类真实事件：
	//   - pending：未解除的等待/卡死接收（参与方真正停住的位置）；
	//   - skipped：因参与方已停住而无法执行的后续真实发送/接收（消息依赖）。
	stalledSet := map[int]bool{}
	stalledOfActor := map[string][]int{}
	for _, idx := range append(append([]int{}, pending...), skipped...) {
		stalledSet[idx] = true
		stalledOfActor[en.events[idx].Actor] = append(stalledOfActor[en.events[idx].Actor], idx)
	}
	for actor := range stalledOfActor {
		sort.Ints(stalledOfActor[actor])
	}

	// prerequisite 返回解除等待 w 所需的真实前置事件下标（去重、按顺序）。
	prerequisite := func(w int) []int {
		e := en.events[w]
		seen := map[int]bool{}
		var out []int
		add := func(idx int) {
			if idx >= 0 && !seen[idx] {
				seen[idx] = true
				out = append(out, idx)
			}
		}
		switch e.Kind {
		case KindReceive:
			if sender, ok := en.msgSender[en.msgRef[w]]; ok {
				add(sender)
			}
		case KindWait:
			for _, rid := range en.waitResources[w] {
				for _, producer := range en.resProducers[rid] {
					add(producer)
				}
			}
		}
		sort.Ints(out)
		return out
	}

	// 对每个 pending 等待，找它经由“别方卡住的前置事件”依赖到的下一个 pending 等待。
	next := map[int]int{}
	for _, w := range pending {
		wActor := en.events[w].Actor
		for _, pre := range prerequisite(w) {
			preActor := en.events[pre].Actor
			if preActor == wActor {
				continue
			}
			// 对端前置事件：若它本身是 stalled 节点则直接连；否则连到该方最早卡点。
			if stalledSet[pre] {
				next[w] = pre
				break
			}
			if blockers := stalledOfActor[preActor]; len(blockers) > 0 {
				next[w] = blockers[0]
				break
			}
		}
	}

	// 发送事件本身不“等待”，但它是接收的前置；补上 stalled 节点之间经由消息的边，
	// 使环能沿 等待 -> (被阻塞的)发送/接收 -> 对端等待 闭合。
	for idx := range stalledSet {
		e := en.events[idx]
		switch e.Kind {
		case KindReceive:
			if sender, ok := en.msgSender[en.msgRef[idx]]; ok && stalledSet[sender] {
				next[idx] = sender
			}
		case KindSend:
			// 发送被阻塞说明其所属方停在更早的卡点上，连到该方最早卡点。
			if blockers := stalledOfActor[e.Actor]; len(blockers) > 0 && blockers[0] != idx {
				next[idx] = blockers[0]
			}
		}
	}

	// 从任一等待出发沿依赖边走，回到已访问节点即得到闭环。
	for _, start := range pending {
		seen := map[int]int{} // 事件 -> 在路径中的次序
		path := make([]int, 0)
		cur := start
		for {
			if pos, ok := seen[cur]; ok {
				ring := append([]int(nil), path[pos:]...)
				sort.Ints(ring)
				return ring, true
			}
			seen[cur] = len(path)
			path = append(path, cur)
			nxt, ok := next[cur]
			if !ok {
				break
			}
			cur = nxt
		}
	}

	// 没有闭环时，仍按真实顺序返回全部 pending 等待作为最接近的依赖切片。
	ring := append([]int(nil), pending...)
	sort.Ints(ring)
	return ring, false
}
