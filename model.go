package main

import (
	"fmt"
	"sort"
)

// MaxInstances 是展开返工链后允许的最大工序实例数。
const MaxInstances = 15

// ResourceMap 描述资源名到数量的映射（需求量或容量）。
type ResourceMap map[string]int

// ReworkSpec 描述失败检验触发的一段返工工序定义。
type ReworkSpec struct {
	ID        string      `json:"id"`
	Duration  int         `json:"duration"`
	Resources ResourceMap `json:"resources"`
}

// TaskSpec 是输入中的一个原始工序。
type TaskSpec struct {
	ID           string       `json:"id"`
	Duration     int          `json:"duration"`
	Resources    ResourceMap  `json:"resources"`
	Predecessors []string     `json:"predecessors"`
	Inspection   string       `json:"inspection"` // "pass" / "fail" / ""(无需检验)
	Rework       []ReworkSpec `json:"rework"`     // 检验失败时依次执行的返工链
}

// Problem 是整个排程输入。
type Problem struct {
	Resources ResourceMap `json:"resources"` // 资源池容量
	Tasks     []TaskSpec  `json:"tasks"`
}

// Activity 是展开后的一个工序实例（原工序或返工实例），身份唯一。
type Activity struct {
	ID       string
	Duration int
	Demands  ResourceMap
	Origin   string // 返工实例记录其源工序 ID；原工序等于自身 ID
	IsRework bool
	PredIDs  []string
	pred     []int
	succ     []int
	cpTail   int // 从本实例到终点（含自身工期）的最长链，用于下界
	index    int
}

// Instance 把输入展开为工序实例图：失败工序后插入返工链，
// 原工序的后继改接到返工链末端，返工与原工序共用同一资源池。
func (p *Problem) Instance() ([]*Activity, error) {
	if len(p.Resources) == 0 {
		return nil, fmt.Errorf("资源池为空：请在 resources 中声明至少一种资源容量")
	}
	for name, cap := range p.Resources {
		if cap <= 0 {
			return nil, fmt.Errorf("资源 %q 容量必须为正整数，得到 %d", name, cap)
		}
	}

	acts := []*Activity{}
	byID := map[string]*Activity{}

	addAct := func(a *Activity) error {
		if a.ID == "" {
			return fmt.Errorf("存在空 ID 的工序实例")
		}
		if _, dup := byID[a.ID]; dup {
			return fmt.Errorf("工序实例 ID %q 重复：返工实例不能覆盖或同名于已有工序", a.ID)
		}
		if a.Duration <= 0 {
			return fmt.Errorf("工序 %q 工期必须为正整数，得到 %d", a.ID, a.Duration)
		}
		for res, d := range a.Demands {
			cap, ok := p.Resources[res]
			if !ok {
				return fmt.Errorf("工序 %q 请求了资源池中不存在的资源 %q", a.ID, res)
			}
			if d <= 0 {
				return fmt.Errorf("工序 %q 对资源 %q 的需求量必须为正整数", a.ID, res)
			}
			if d > cap {
				return fmt.Errorf("工序 %q 对资源 %q 需求 %d 超过资源池容量 %d", a.ID, res, d, cap)
			}
		}
		a.index = len(acts)
		acts = append(acts, a)
		byID[a.ID] = a
		return nil
	}

	// 第一遍：建立全部实例（原工序 + 失败触发的返工链）。
	type reworkTail struct{ last string }
	tails := map[string]reworkTail{} // 失败工序 ID -> 返工链末端实例 ID
	for i := range p.Tasks {
		t := &p.Tasks[i]
		orig := &Activity{
			ID:       t.ID,
			Duration: t.Duration,
			Demands:  copyMap(t.Resources),
			Origin:   t.ID,
			PredIDs:  append([]string(nil), t.Predecessors...),
		}
		if err := addAct(orig); err != nil {
			return nil, err
		}
		switch t.Inspection {
		case "", "pass":
			if len(t.Rework) > 0 && t.Inspection == "pass" {
				return nil, fmt.Errorf("工序 %q 检验通过却声明了返工链，输入矛盾", t.ID)
			}
		case "fail":
			if len(t.Rework) == 0 {
				return nil, fmt.Errorf("工序 %q 检验失败但未给出返工链", t.ID)
			}
			prev := t.ID
			for j, r := range t.Rework {
				ra := &Activity{
					ID:       r.ID,
					Duration: r.Duration,
					Demands:  copyMap(r.Resources),
					Origin:   t.ID,
					IsRework: true,
					PredIDs:  []string{prev},
				}
				if err := addAct(ra); err != nil {
					return nil, fmt.Errorf("工序 %q 的第 %d 段返工无效：%v", t.ID, j+1, err)
				}
				prev = r.ID
			}
			tails[t.ID] = reworkTail{last: prev}
		default:
			return nil, fmt.Errorf("工序 %q 的检验结果 %q 无法识别（应为 pass/fail）", t.ID, t.Inspection)
		}
	}

	if len(acts) > MaxInstances {
		return nil, fmt.Errorf("展开返工链后共 %d 个工序实例，超过上限 %d", len(acts), MaxInstances)
	}

	// 第二遍：把失败工序的原始后继改接到返工链末端，保留依赖语义。
	for i := range p.Tasks {
		t := &p.Tasks[i]
		for _, pid := range t.Predecessors {
			if tail, failed := tails[pid]; failed {
				byID[t.ID].PredIDs = replaceString(byID[t.ID].PredIDs, pid, tail.last)
			}
		}
	}

	// 解析依赖索引并检测环。
	for _, a := range acts {
		seen := map[string]bool{}
		for _, pid := range a.PredIDs {
			p, ok := byID[pid]
			if !ok {
				return nil, fmt.Errorf("工序 %q 依赖了不存在的工序 %q", a.ID, pid)
			}
			if p == a {
				return nil, fmt.Errorf("工序 %q 不能依赖自身", a.ID)
			}
			if seen[pid] {
				continue
			}
			seen[pid] = true
			a.pred = append(a.pred, p.index)
			p.succ = append(p.succ, a.index)
		}
	}
	if err := topoAndCriticalTail(acts); err != nil {
		return nil, err
	}
	return acts, nil
}

func copyMap(m ResourceMap) ResourceMap {
	out := ResourceMap{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func replaceString(xs []string, old, new string) []string {
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if x == old {
			out = append(out, new)
		} else {
			out = append(out, x)
		}
	}
	return out
}

// topoAndCriticalTail 做拓扑排序（检环）并计算每个实例到终点的最长链长。
func topoAndCriticalTail(acts []*Activity) error {
	indeg := make([]int, len(acts))
	for _, a := range acts {
		indeg[a.index] = len(a.pred)
	}
	queue := []int{}
	for _, a := range acts {
		if indeg[a.index] == 0 {
			queue = append(queue, a.index)
		}
	}
	order := []int{}
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		order = append(order, i)
		for _, s := range acts[i].succ {
			indeg[s]--
			if indeg[s] == 0 {
				queue = append(queue, s)
			}
		}
	}
	if len(order) != len(acts) {
		return fmt.Errorf("依赖图存在环（含返工链展开后），无法排程")
	}
	for k := len(order) - 1; k >= 0; k-- {
		a := acts[order[k]]
		best := 0
		for _, s := range a.succ {
			if acts[s].cpTail > best {
				best = acts[s].cpTail
			}
		}
		a.cpTail = a.Duration + best
	}
	return nil
}

// resourceNames 返回排序后的资源名列表，保证输出稳定。
func resourceNames(cap ResourceMap) []string {
	names := make([]string, 0, len(cap))
	for n := range cap {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
