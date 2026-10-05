package main

import (
	"fmt"
	"sort"
)

// DepCheck 是一条依赖核验结果。
type DepCheck struct {
	Predecessor string `json:"predecessor"`
	Successor   string `json:"successor"`
	PredFinish  int    `json:"pred_finish"`
	SuccStart   int    `json:"succ_start"`
	OK          bool   `json:"ok"`
}

// UsageInterval 是资源被单个实例占用的一段区间。
type UsageInterval struct {
	Resource string `json:"resource"`
	Task     string `json:"task"`
	Start    int    `json:"start"`
	Finish   int    `json:"finish"`
	Load     int    `json:"load"`
}

// Report 是完整输出：排程、资源占用区间与依赖核验。
type Report struct {
	Makespan   int             `json:"makespan"`
	Optimal    bool            `json:"optimal"`
	Instances  int             `json:"instances"`
	Entries    []ScheduleEntry `json:"schedule"`
	Usage      []UsageInterval `json:"resource_usage"`
	DepChecks  []DepCheck      `json:"dependency_checks"`
	DepsOK     bool            `json:"dependencies_ok"`
	ResourceOK bool            `json:"resources_ok"`
}

// BuildReport 由排程结果生成核验报告：逐条检查依赖约束，
// 并按事件区间检查资源占用不超过资源池容量。
func BuildReport(acts []*Activity, capacity ResourceMap, sol Solution) (Report, error) {
	byID := map[string]*Activity{}
	for _, a := range acts {
		byID[a.ID] = a
	}
	start := map[string]int{}
	for _, e := range sol.Entries {
		if _, dup := start[e.ID]; dup {
			return Report{}, fmt.Errorf("排程结果中实例 %q 出现多次", e.ID)
		}
		start[e.ID] = e.Start
	}
	if len(start) != len(acts) {
		return Report{}, fmt.Errorf("排程结果覆盖 %d 个实例，但实例图共 %d 个", len(start), len(acts))
	}

	rep := Report{
		Makespan:  sol.Makespan,
		Optimal:   sol.Optimal,
		Instances: len(acts),
		Entries:   sol.Entries,
		DepsOK:    true,
	}

	// 依赖核验：每条前驱边要求 succ.start >= pred.finish。
	for _, a := range acts {
		for _, p := range a.pred {
			pred := acts[p]
			chk := DepCheck{
				Predecessor: pred.ID,
				Successor:   a.ID,
				PredFinish:  start[pred.ID] + pred.Duration,
				SuccStart:   start[a.ID],
			}
			chk.OK = chk.SuccStart >= chk.PredFinish
			if !chk.OK {
				rep.DepsOK = false
			}
			rep.DepChecks = append(rep.DepChecks, chk)
		}
	}
	sort.Slice(rep.DepChecks, func(i, j int) bool {
		if rep.DepChecks[i].Successor != rep.DepChecks[j].Successor {
			return rep.DepChecks[i].Successor < rep.DepChecks[j].Successor
		}
		return rep.DepChecks[i].Predecessor < rep.DepChecks[j].Predecessor
	})

	// 资源占用区间：按资源汇总每个实例的占用段。
	for _, e := range sol.Entries {
		a := byID[e.ID]
		for _, res := range resourceNames(a.Demands) {
			rep.Usage = append(rep.Usage, UsageInterval{
				Resource: res,
				Task:     e.ID,
				Start:    e.Start,
				Finish:   e.Start + a.Duration,
				Load:     a.Demands[res],
			})
		}
	}
	sort.Slice(rep.Usage, func(i, j int) bool {
		if rep.Usage[i].Resource != rep.Usage[j].Resource {
			return rep.Usage[i].Resource < rep.Usage[j].Resource
		}
		if rep.Usage[i].Start != rep.Usage[j].Start {
			return rep.Usage[i].Start < rep.Usage[j].Start
		}
		return rep.Usage[i].Task < rep.Usage[j].Task
	})

	// 资源容量核验：扫描每个资源的事件点，任何区间负载不得超过容量。
	rep.ResourceOK = true
	for _, res := range resourceNames(capacity) {
		points := map[int]bool{}
		for _, u := range rep.Usage {
			if u.Resource == res {
				points[u.Start] = true
				points[u.Finish] = true
			}
		}
		pts := make([]int, 0, len(points))
		for p := range points {
			pts = append(pts, p)
		}
		sort.Ints(pts)
		for k := 0; k+1 < len(pts); k++ {
			lo, hi := pts[k], pts[k+1]
			load := 0
			for _, u := range rep.Usage {
				if u.Resource == res && u.Start <= lo && u.Finish >= hi {
					load += u.Load
				}
			}
			if load > capacity[res] {
				rep.ResourceOK = false
				return rep, fmt.Errorf("资源 %q 在区间 [%d,%d) 负载 %d 超过容量 %d",
					res, lo, hi, load, capacity[res])
			}
		}
	}
	return rep, nil
}
