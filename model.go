package main

import (
	"fmt"
	"sort"
)

// MaxInstances 是求解器允许的工序实例总数上限（含返工实例）。
const MaxInstances = 15

// JobInput 描述原始工序。
type JobInput struct {
	ID      string         `json:"id"`
	Dur     int            `json:"duration"`
	Demands map[string]int `json:"demands"`
	Deps    []string       `json:"deps"`
}

// ReworkStep 描述返工链中的一步。
type ReworkStep struct {
	ID      string         `json:"id"`
	Dur     int            `json:"duration"`
	Demands map[string]int `json:"demands"`
}

// FailureInput 描述某工序检验失败后必须执行的指定返工链。
type FailureInput struct {
	Job    string       `json:"job"`
	Rework []ReworkStep `json:"rework"`
}

// Input 是完整问题输入：资源池、原工序、完整失败结果。
type Input struct {
	Resources map[string]int `json:"resources"`
	Jobs      []JobInput     `json:"jobs"`
	Failures  []FailureInput `json:"failures"`
}

// Instance 是排程的最小单位。原工序与每个返工步都是独立实例，
// 各自保留身份（ID 唯一，Origin 指向来源工序），互不覆盖。
type Instance struct {
	ID       string         `json:"id"`
	Origin   string         `json:"origin"`
	IsRework bool           `json:"rework"`
	Dur      int            `json:"duration"`
	Demands  map[string]int `json:"demands"`
	Preds    []string       `json:"preds"`
}

// expand 根据完整失败结果把返工链嵌入依赖图：
// 失败工序 J 的后继改挂到返工链末端，链本身按 J -> R1 -> R2 ... 串接。
func expand(in *Input) ([]Instance, []string, map[string]int, error) {
	if len(in.Resources) == 0 {
		return nil, nil, nil, fmt.Errorf("resources 不能为空")
	}
	resNames := make([]string, 0, len(in.Resources))
	for name, cap := range in.Resources {
		if cap <= 0 {
			return nil, nil, nil, fmt.Errorf("资源 %q 容量必须为正整数", name)
		}
		resNames = append(resNames, name)
	}
	sort.Strings(resNames)
	caps := make(map[string]int, len(in.Resources))
	for k, v := range in.Resources {
		caps[k] = v
	}

	jobIdx := make(map[string]int, len(in.Jobs))
	insts := make([]Instance, 0, len(in.Jobs))
	for _, j := range in.Jobs {
		if j.ID == "" {
			return nil, nil, nil, fmt.Errorf("工序 id 不能为空")
		}
		if _, dup := jobIdx[j.ID]; dup {
			return nil, nil, nil, fmt.Errorf("工序 id %q 重复", j.ID)
		}
		if j.Dur <= 0 {
			return nil, nil, nil, fmt.Errorf("工序 %q 的 duration 必须为正整数", j.ID)
		}
		jobIdx[j.ID] = len(insts)
		insts = append(insts, Instance{
			ID: j.ID, Origin: j.ID, Dur: j.Dur,
			Demands: copyDemands(j.Demands), Preds: append([]string(nil), j.Deps...),
		})
	}

	failed := make(map[string]bool, len(in.Failures))
	for _, f := range in.Failures {
		if _, ok := jobIdx[f.Job]; !ok {
			return nil, nil, nil, fmt.Errorf("失败结果引用了不存在的工序 %q", f.Job)
		}
		if failed[f.Job] {
			return nil, nil, nil, fmt.Errorf("工序 %q 的失败结果重复给出", f.Job)
		}
		failed[f.Job] = true
		if len(f.Rework) == 0 {
			return nil, nil, nil, fmt.Errorf("工序 %q 检验失败但返工链为空", f.Job)
		}
		prev := f.Job
		for _, step := range f.Rework {
			if step.ID == "" {
				return nil, nil, nil, fmt.Errorf("工序 %q 的返工步 id 不能为空", f.Job)
			}
			if step.Dur <= 0 {
				return nil, nil, nil, fmt.Errorf("返工步 %q 的 duration 必须为正整数", step.ID)
			}
			instID := f.Job + "#" + step.ID
			if _, dup := jobIdx[instID]; dup {
				return nil, nil, nil, fmt.Errorf("返工实例 id %q 与已有实例冲突", instID)
			}
			jobIdx[instID] = len(insts)
			insts = append(insts, Instance{
				ID: instID, Origin: f.Job, IsRework: true, Dur: step.Dur,
				Demands: copyDemands(step.Demands), Preds: []string{prev},
			})
			prev = instID
		}
	}

	if len(insts) > MaxInstances {
		return nil, nil, nil, fmt.Errorf("展开后共 %d 个工序实例，超过上限 %d", len(insts), MaxInstances)
	}

	// 校验依赖引用并重挂失败工序的后继到返工链末端。
	tail := make(map[string]string, len(insts)) // 工序/实例 -> 其出边起点（失败工序为返工链末端）
	for _, inst := range insts {
		tail[inst.ID] = inst.ID
	}
	for _, f := range in.Failures {
		tail[f.Job] = f.Job + "#" + f.Rework[len(f.Rework)-1].ID
	}
	for i := range insts {
		inst := &insts[i]
		if inst.IsRework {
			continue // 返工链内部依赖在展开时已确定，不做重挂
		}
		for d, dep := range inst.Preds {
			if _, ok := jobIdx[dep]; !ok {
				return nil, nil, nil, fmt.Errorf("实例 %q 依赖了不存在的工序 %q", inst.ID, dep)
			}
			inst.Preds[d] = tail[dep]
			if inst.Preds[d] == inst.ID {
				return nil, nil, nil, fmt.Errorf("实例 %q 不能依赖自身", inst.ID)
			}
		}
	}

	// 资源需求校验：名称必须存在且不得超过资源池容量。
	for _, inst := range insts {
		for res, d := range inst.Demands {
			cap, ok := caps[res]
			if !ok {
				return nil, nil, nil, fmt.Errorf("实例 %q 引用了不存在的资源 %q", inst.ID, res)
			}
			if d <= 0 {
				return nil, nil, nil, fmt.Errorf("实例 %q 对资源 %q 的需求必须为正整数", inst.ID, res)
			}
			if d > cap {
				return nil, nil, nil, fmt.Errorf("实例 %q 对资源 %q 的需求 %d 超过容量 %d", inst.ID, res, d, cap)
			}
		}
	}

	if hasCycle(insts) {
		return nil, nil, nil, fmt.Errorf("依赖图存在环")
	}
	return insts, resNames, caps, nil
}

func copyDemands(d map[string]int) map[string]int {
	out := make(map[string]int, len(d))
	for k, v := range d {
		out[k] = v
	}
	return out
}

func hasCycle(insts []Instance) bool {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	idx := make(map[string]int, len(insts))
	for i, inst := range insts {
		idx[inst.ID] = i
	}
	color := make([]int, len(insts))
	var visit func(i int) bool
	visit = func(i int) bool {
		color[i] = gray
		for _, p := range insts[i].Preds {
			j := idx[p]
			if color[j] == gray {
				return true
			}
			if color[j] == white && visit(j) {
				return true
			}
		}
		color[i] = black
		return false
	}
	for i := range insts {
		if color[i] == white && visit(i) {
			return true
		}
	}
	return false
}
