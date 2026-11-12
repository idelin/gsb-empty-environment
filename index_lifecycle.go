package netsec

import "sort"

// Revoke 显式撤销 ID 的当前有效世代。
//
// 失效沿依赖链传播并使下游全部受限，在途操作按例外策略终止。
// 对已失效例外重复撤销是幂等的：返回 nil 事件且不产生新墓碑。
// 例外不存在时返回 ErrExceptionNotFound。
func (idx *DependencyIndex) Revoke(id, note string) (*InvalidationEvent, error) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	exc := idx.exceptions[id]
	if exc == nil {
		return nil, wrapID(ErrExceptionNotFound, id)
	}
	if !exc.Active() {
		return nil, nil
	}
	if note == "" {
		note = "exception revoked"
	}
	event := idx.invalidateLocked(exc, ReasonRevoked, note)
	return cloneEvent(event), nil
}

// SweepExpired 依据时钟扫描所有到期且仍有效的例外世代并使其失效。
// 返回本次新产生的失效事件，确定性按例外 ID 与世代排序。
// 重复扫描不会重复失效，也不会重复中止在途操作。
func (idx *DependencyIndex) SweepExpired() []InvalidationEvent {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	now := idx.clock.Now()
	type genRef struct {
		id string
		g  int
	}
	var due []genRef
	for id, gens := range idx.exceptionGens {
		for gen, exc := range gens {
			if exc.Active() && exc.ExpiresAt != 0 && exc.ExpiresAt <= now {
				due = append(due, genRef{id, gen})
			}
		}
	}
	sort.Slice(due, func(i, j int) bool {
		ti := idx.exceptionGens[due[i].id][due[i].g].ExpiresAt
		tj := idx.exceptionGens[due[j].id][due[j].g].ExpiresAt
		if ti != tj {
			return ti < tj
		}
		if due[i].id != due[j].id {
			return due[i].id < due[j].id
		}
		return due[i].g < due[j].g
	})

	events := make([]InvalidationEvent, 0, len(due))
	for _, ref := range due {
		exc := idx.exceptionGens[ref.id][ref.g]
		if event := idx.invalidateLocked(exc, ReasonExpired, "exception expired"); event != nil {
			events = append(events, *event)
		}
	}
	return events
}

// invalidateLocked 执行单次例外失效：写墓碑并沿反向依赖图 BFS 传播。
// 重复失效在入口处被 Active 判断拦截，保证幂等。
func (idx *DependencyIndex) invalidateLocked(exc *Exception, reason InvalidationReason, note string) *InvalidationEvent {
	if !exc.Active() {
		return nil
	}
	at := idx.clock.Now()
	if reason == ReasonRevoked {
		exc.state = ExceptionRevoked
	} else {
		exc.state = ExceptionExpired
	}
	exc.invalidAt = at

	event := &InvalidationEvent{
		Version: exc.Version(),
		Reason:  reason,
		At:      at,
		Note:    note,
	}
	idx.events = append(idx.events, *event)

	// 种子：直接依赖该例外版本的资源；菱形依赖在此天然去重。
	visited := make(map[string]bool)
	queue := make([]string, 0)
	for id, entry := range idx.resources {
		if containsVersion(entry.excDeps, exc.Version()) {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)

	for len(queue) > 0 {
		rid := queue[0]
		queue = queue[1:]
		if visited[rid] {
			continue
		}
		visited[rid] = true
		entry := idx.resources[rid]
		idx.applyCauseLocked(entry, exc, at, note)

		// 沿资源->上游边的反向继续传播（下游依赖本资源）。
		for id, candidate := range idx.resources {
			if !visited[id] && containsString(candidate.upstreams, rid) {
				queue = append(queue, id)
			}
		}
		sort.Strings(queue)
	}
	return event
}

// applyCauseLocked 把一个失效例外施加到资源：合并原因、升级终止
// 策略，并在 abort 时强制中止全部在途操作。
//
// 策略升级规则：任一失效原因为 abort，资源即升级为 abort；
// 首次受限且原因是 drain 时进入 drain。菱形依赖只合并一次原因。
func (idx *DependencyIndex) applyCauseLocked(entry *resourceEntry, exc *Exception, at Time, note string) {
	firstCause := !entry.restricted
	entry.restricted = true
	if !containsVersion(entry.causes, exc.Version()) {
		entry.causes = append(entry.causes, exc.Version())
		sort.Slice(entry.causes, func(i, j int) bool {
			return versionLess(entry.causes[i], entry.causes[j])
		})
	}

	switch {
	case exc.Policy == TerminateAbort:
		entry.policy = TerminateAbort
	case firstCause:
		entry.policy = TerminateDrain
	}

	if exc.Policy != TerminateAbort {
		return
	}
	abortNote := "aborted by invalidation of " + exc.Version().String() +
		" (" + note + ")"
	for opID, op := range entry.runningOps {
		if op.Terminated() {
			continue
		}
		op.state = OpAborted
		op.terminatedAt = at
		op.abortNote = abortNote
		delete(entry.runningOps, opID)
	}
}

// recomputeAllLocked 在注册或 Rebase 资源后全量重算受限状态。
// 它依据当前所有失效墓碑重建原因集合与策略，因此资源 Rebase 到
// 有效新世代后可以解除受限，反之新资源注册在失效链上会立即受限。
// 该重算不回溯处理历史操作：已终止的操作保持其明确终止状态。
func (idx *DependencyIndex) recomputeAllLocked() {
	type deadRef struct {
		version ExceptionVersion
		policy  TerminationPolicy
	}
	var dead []deadRef
	for id, gens := range idx.exceptionGens {
		for gen, exc := range gens {
			if !exc.Active() {
				dead = append(dead, deadRef{
					version: ExceptionVersion{ID: id, Generation: gen},
					policy:  exc.Policy,
				})
			}
		}
	}
	sort.Slice(dead, func(i, j int) bool { return versionLess(dead[i].version, dead[j].version) })

	for _, entry := range idx.resources {
		entry.restricted = false
		entry.policy = TerminateUnset
		entry.causes = entry.causes[:0]
	}

	reach := make(map[string]bool)
	for _, d := range dead {
		exc := idx.exceptionGens[d.version.ID][d.version.Generation]
		for id := range idx.reachOfLocked(d.version, reach) {
			entry := idx.resources[id]
			firstCause := !entry.restricted
			entry.restricted = true
			if !containsVersion(entry.causes, d.version) {
				entry.causes = append(entry.causes, d.version)
			}
			switch {
			case exc.Policy == TerminateAbort:
				entry.policy = TerminateAbort
			case firstCause:
				entry.policy = TerminateDrain
			}
		}
	}
	for _, entry := range idx.resources {
		sort.Slice(entry.causes, func(i, j int) bool {
			return versionLess(entry.causes[i], entry.causes[j])
		})
	}
}

// reachOfLocked 计算直接或传递依赖指定例外版本的全部资源 ID。
// 复用传入的 scratch map 以减少分配。
func (idx *DependencyIndex) reachOfLocked(v ExceptionVersion, scratch map[string]bool) map[string]bool {
	for k := range scratch {
		delete(scratch, k)
	}
	queue := make([]string, 0)
	for id, entry := range idx.resources {
		if containsVersion(entry.excDeps, v) {
			queue = append(queue, id)
		}
	}
	for len(queue) > 0 {
		rid := queue[0]
		queue = queue[1:]
		if scratch[rid] {
			continue
		}
		scratch[rid] = true
		for id, candidate := range idx.resources {
			if !scratch[id] && containsString(candidate.upstreams, rid) {
				queue = append(queue, id)
			}
		}
	}
	return scratch
}
