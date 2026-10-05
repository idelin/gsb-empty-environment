package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"sort"
)

// ScheduledInstance 是排程结果中的一个工序实例（保留完整身份）。
type ScheduledInstance struct {
	ID       string         `json:"id"`
	Origin   string         `json:"origin"`
	IsRework bool           `json:"rework"`
	Start    int            `json:"start"`
	End      int            `json:"end"`
	Duration int            `json:"duration"`
	Demands  map[string]int `json:"demands"`
}

// ResourceInterval 是资源池上的一段占用区间。
type ResourceInterval struct {
	Instance string `json:"instance"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
	Amount   int    `json:"amount"`
}

// Output 是完整求解输出。
type Output struct {
	Makespan          int                           `json:"makespan"`
	Optimal           bool                          `json:"optimal"`
	Schedule          []ScheduledInstance           `json:"schedule"`
	ResourceIntervals map[string][]ResourceInterval `json:"resource_intervals"`
	Verification      Verification                  `json:"verification"`
}

func main() {
	data, err := readInput(os.Args[1:])
	if err != nil {
		fatal(err)
	}
	var in Input
	if err := json.Unmarshal(data, &in); err != nil {
		fatal(fmt.Errorf("输入 JSON 解析失败: %v", err))
	}
	insts, resNames, caps, err := expand(&in)
	if err != nil {
		fatal(err)
	}

	s := newSolver(insts, resNames, caps)
	starts, makespan := s.solve()

	out := Output{
		Makespan:          makespan,
		Optimal:           true,
		ResourceIntervals: make(map[string][]ResourceInterval),
	}
	for i, inst := range insts {
		out.Schedule = append(out.Schedule, ScheduledInstance{
			ID: inst.ID, Origin: inst.Origin, IsRework: inst.IsRework,
			Start: starts[i], End: starts[i] + inst.Dur,
			Duration: inst.Dur, Demands: inst.Demands,
		})
	}
	sort.Slice(out.Schedule, func(a, b int) bool {
		if out.Schedule[a].Start != out.Schedule[b].Start {
			return out.Schedule[a].Start < out.Schedule[b].Start
		}
		return out.Schedule[a].ID < out.Schedule[b].ID
	})
	for _, res := range resNames {
		for i, inst := range insts {
			d := inst.Demands[res]
			if d == 0 {
				continue
			}
			out.ResourceIntervals[res] = append(out.ResourceIntervals[res], ResourceInterval{
				Instance: inst.ID, Start: starts[i], End: starts[i] + inst.Dur, Amount: d,
			})
		}
		sort.Slice(out.ResourceIntervals[res], func(a, b int) bool {
			iv := out.ResourceIntervals[res]
			if iv[a].Start != iv[b].Start {
				return iv[a].Start < iv[b].Start
			}
			return iv[a].Instance < iv[b].Instance
		})
	}
	out.Verification = verify(insts, resNames, caps, starts)

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fatal(err)
	}
	if !out.Verification.DependenciesOK || !out.Verification.ResourcesOK || !out.Verification.IdentityOK {
		os.Exit(2)
	}
}

func readInput(args []string) ([]byte, error) {
	if len(args) > 1 {
		return nil, fmt.Errorf("用法: reworksched [input.json]（缺省从标准输入读取）")
	}
	if len(args) == 1 {
		return ioutil.ReadFile(args[0])
	}
	return ioutil.ReadAll(os.Stdin)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "错误:", err)
	os.Exit(1)
}
