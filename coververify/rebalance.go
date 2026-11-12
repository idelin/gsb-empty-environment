package coververify

import "fmt"

// rebalance 对全部有效原件做不动点重核：冲突隔离优先，随后按可达链逐跳重放。
// 每轮先重置原件状态，保证迟到回执或冲突解除能够回溯纠正历史裁决。
func (v *Verifier) rebalance() {
	active := make([]*entry, 0, len(v.entries))
	for _, e := range v.entries {
		if !e.invalid && !e.dup {
			active = append(active, e)
		}
	}

	// 同一转换边出现互异输出即冲突，冲突边整体隔离且永不参与链的续接。
	conflictEdges := make(map[TransitionKey]bool)
	edgeOutputs := make(map[TransitionKey]map[Digest]struct{})
	for _, e := range active {
		set := edgeOutputs[e.edge]
		if set == nil {
			set = make(map[Digest]struct{})
			edgeOutputs[e.edge] = set
		}
		set[e.receipt.Output] = struct{}{}
		if len(set) > 1 {
			conflictEdges[e.edge] = true
		}
	}

	reachable := make(map[string]map[Digest]bool)
	addReachable := func(model string, state Digest) {
		set := reachable[model]
		if set == nil {
			set = make(map[Digest]bool)
			reachable[model] = set
		}
		set[state] = true
	}
	for model, roots := range v.roots {
		for state := range roots {
			addReachable(model, state)
		}
	}

	const maxPasses = 1024
	for pass := 0; pass < maxPasses; pass++ {
		for _, e := range active {
			e.status = StatusPending
			e.reject = false
			e.detail = ""
		}

		progress := false
		for _, e := range active {
			if conflictEdges[e.edge] {
				e.status = StatusConflict
				e.detail = fmt.Sprintf("conflicting outputs reported on edge %s", formatEdge(e.edge))
				continue
			}

			modelReach, ok := reachable[e.receipt.ModelVersion]
			if !ok || !modelReach[e.receipt.PrevState] {
				// 断链：保留待定，等待前驱链补齐。
				e.status = StatusPending
				continue
			}

			replay, err := v.replay(e.edge)
			if err != nil {
				e.status = StatusErrored
				e.detail = err.Error()
				continue
			}

			if replay.Output != e.receipt.Output {
				e.status = StatusMismatch
				e.detail = fmt.Sprintf("replay output %q != receipt output %q", replay.Output, e.receipt.Output)
				continue
			}

			e.status = StatusCovered
			e.reject = replay.Rejected
			if !modelReach[replay.NextState] {
				progress = true
				addReachable(e.receipt.ModelVersion, replay.NextState)
			}
		}

		if !progress {
			break
		}
	}

	// 重复回执实时镜像原件状态，保证 Snapshot/Stats 看到的裁决一致。
	for _, e := range v.entries {
		if e.dup {
			if master, ok := v.byUID[e.dupOf]; ok {
				e.status = master.status
				e.reject = master.reject
				e.detail = master.detail
			}
		}
	}
}

// replay 调用宿主确定性回调并缓存成功结果。
//
// 成功结果按转换边缓存：同一 (模型版本, 前状态, 输入) 重放多次必须得到同一答案，
// 缓存同时约束了回调的确定性。回调返回错误或 panic 时不缓存，返回包装错误，该边
// 本次裁决为 [StatusErrored]，回调修复后的下一次 Submit 会重新调用并重获覆盖。
func (v *Verifier) replay(edge TransitionKey) (result ReplayResult, err error) {
	key := replayKey{model: edge.ModelVersion, prev: edge.PrevState, input: edge.Input}
	if cached, ok := v.replayCache[key]; ok {
		return cached, nil
	}

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("coververify: oracle panicked: %v", r)
			result = ReplayResult{}
		}
	}()

	out := v.oracle(edge.ModelVersion, edge.PrevState, edge.Input)
	if out.Err != nil {
		return ReplayResult{}, fmt.Errorf("coververify: oracle failed: %w", out.Err)
	}
	if out.NextState == "" || out.Output == "" {
		return ReplayResult{}, fmt.Errorf("coververify: oracle returned empty next state or output for %s", formatEdge(edge))
	}

	v.replayCache[key] = out
	return out, nil
}
