package netsec

import "fmt"

// maxStates 是探索的状态数上限，防止配置失控时耗尽内存。
const maxStates = 20_000_000

// Violation 是一次不变式违例的见证：从初始状态到违例迁移的最短轨迹。
type Violation struct {
	Trace []Step // 依次执行的迁移；最后一步即违例迁移
}

// Depth 返回违例发生时的迁移步数。
func (v Violation) Depth() int { return len(v.Trace) }

// Result 汇总一次状态空间探索。
type Result struct {
	Reachable int        // 可达状态数（不含违例迁移之后的状态）
	Violation *Violation // 最短违例轨迹；不变式成立时为 nil
}

// Explore 从初始状态做广度优先探索，检查不变式：
// 每个已交付的明文，其密钥代在交付时刻必须被双方共同认可。
// 由于按层扩展，返回的违例轨迹一定是最短的。
func Explore(cfg Config) (Result, error) {
	start := Initial(cfg)
	type parent struct {
		prev State
		step Step
	}
	visited := make(map[State]struct{}, 1<<16)
	parents := make(map[State]parent, 1<<16)
	visited[start] = struct{}{}
	queue := []State{start}

	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		for _, step := range Successors(cfg, s) {
			if step.Violates {
				// 回溯父链，重建从初始状态到 s 的轨迹。
				var rev []Step
				for cur := s; cur != start; {
					p := parents[cur]
					rev = append(rev, p.step)
					cur = p.prev
				}
				trace := make([]Step, 0, len(rev)+1)
				for i := len(rev) - 1; i >= 0; i-- {
					trace = append(trace, rev[i])
				}
				trace = append(trace, step)
				return Result{Reachable: len(visited), Violation: &Violation{Trace: trace}}, nil
			}
			if _, ok := visited[step.Next]; ok {
				continue
			}
			if len(visited) >= maxStates {
				return Result{}, fmt.Errorf("state space exceeds %d states", maxStates)
			}
			visited[step.Next] = struct{}{}
			parents[step.Next] = parent{prev: s, step: step}
			queue = append(queue, step.Next)
		}
	}
	return Result{Reachable: len(visited)}, nil
}

// CountReachable 探索完整状态空间（违例迁移不展开、不计入），
// 返回可达状态总数与违例首次出现的深度（无违例时深度为 -1）。
// 供与显式小状态参考实现交叉核对。
func CountReachable(cfg Config) (states int, violDepth int, err error) {
	start := Initial(cfg)
	visited := map[State]struct{}{start: {}}
	type item struct {
		s State
		d int
	}
	queue := []item{{start, 0}}
	violDepth = -1
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		for _, step := range Successors(cfg, it.s) {
			if step.Violates {
				if violDepth < 0 {
					violDepth = it.d + 1
				}
				continue
			}
			if _, ok := visited[step.Next]; ok {
				continue
			}
			if len(visited) >= maxStates {
				return 0, -1, fmt.Errorf("state space exceeds %d states", maxStates)
			}
			visited[step.Next] = struct{}{}
			queue = append(queue, item{step.Next, it.d + 1})
		}
	}
	return len(visited), violDepth, nil
}

// Replay 从初始状态重放轨迹，校验每一步都是模型的合法迁移，
// 并返回最终状态。供测试与宿主核对违例轨迹使用。
func Replay(cfg Config, trace []Step) (State, error) {
	s := Initial(cfg)
	for i, step := range trace {
		found := false
		for _, cand := range Successors(cfg, s) {
			if cand.Next == step.Next {
				if cand.Violates != step.Violates {
					return s, fmt.Errorf("step %d %q: violation flag mismatch", i, step.Name)
				}
				found = true
				break
			}
		}
		if !found {
			return s, fmt.Errorf("step %d %q: not a legal transition", i, step.Name)
		}
		s = step.Next
	}
	return s, nil
}
