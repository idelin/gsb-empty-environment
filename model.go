package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
)

// Event 是一个带已知区间误差的时钟事件：真实时刻落在 [Lo, Hi] 内。
type Event struct {
	Name string
	Lo   float64
	Hi   float64
}

// Task 描述一个任务的起止与资源等待事件（Request/Acquire 可空）。
type Task struct {
	Name    string
	Request string // 资源请求事件（可选）
	Acquire string // 资源获取事件（可选），Acquire-Request 即排队时间
	Start   string // 开始执行事件
	End     string // 结束事件
}

// Dep 表示依赖：After 事件完成后 Before 事件才能发生。
type Dep struct {
	After  string
	Before string
	Label  string
}

// Input 是工具的完整输入。
type Input struct {
	Events    []Event
	Tasks     []Task
	Deps      []Dep
	SpanBegin string // 总耗时起点事件
	SpanEnd   string // 总耗时终点事件
}

type rawEvent struct {
	Name     string    `json:"name"`
	Time     *float64  `json:"time"`     // 观测时刻
	Err      *float64  `json:"err"`      // 已知时钟误差，区间 [Time-Err, Time+Err]
	Interval []float64 `json:"interval"` // 或直接给 [lo, hi]
}

type rawTask struct {
	Name    string `json:"name"`
	Request string `json:"request"`
	Acquire string `json:"acquire"`
	Start   string `json:"start"`
	End     string `json:"end"`
}

type rawDep struct {
	After  string `json:"after"`
	Before string `json:"before"`
	Label  string `json:"label"`
}

type rawSpan struct {
	Begin string `json:"begin"`
	End   string `json:"end"`
}

type rawInput struct {
	Events []rawEvent `json:"events"`
	Tasks  []rawTask  `json:"tasks"`
	Deps   []rawDep   `json:"dependencies"`
	Span   rawSpan    `json:"span"`
}

// LoadInput 从 JSON 文件读取输入（path 为 "-" 时读标准输入）。
func LoadInput(path string) (*Input, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = ioutil.ReadAll(os.Stdin)
	} else {
		data, err = ioutil.ReadFile(path)
	}
	if err != nil {
		return nil, err
	}
	var ri rawInput
	if err := json.Unmarshal(data, &ri); err != nil {
		return nil, fmt.Errorf("JSON 解析失败: %v", err)
	}
	in := &Input{SpanBegin: ri.Span.Begin, SpanEnd: ri.Span.End}
	seen := map[string]bool{}
	for _, re := range ri.Events {
		if re.Name == "" {
			return nil, fmt.Errorf("存在未命名事件")
		}
		if seen[re.Name] {
			return nil, fmt.Errorf("事件 %q 重复定义", re.Name)
		}
		seen[re.Name] = true
		var lo, hi float64
		switch {
		case re.Time != nil:
			e := 0.0
			if re.Err != nil {
				e = *re.Err
			}
			if e < 0 {
				return nil, fmt.Errorf("事件 %q 的误差不能为负", re.Name)
			}
			lo, hi = *re.Time-e, *re.Time+e
		case len(re.Interval) == 2:
			lo, hi = re.Interval[0], re.Interval[1]
		default:
			return nil, fmt.Errorf("事件 %q 需要 time(+err) 或 interval [lo,hi]", re.Name)
		}
		if lo > hi {
			return nil, fmt.Errorf("事件 %q 的区间下界大于上界", re.Name)
		}
		in.Events = append(in.Events, Event{Name: re.Name, Lo: lo, Hi: hi})
	}
	for _, rt := range ri.Tasks {
		if rt.Start == "" || rt.End == "" {
			return nil, fmt.Errorf("任务 %q 必须给出 start 和 end 事件", rt.Name)
		}
		in.Tasks = append(in.Tasks, Task{Name: rt.Name, Request: rt.Request, Acquire: rt.Acquire, Start: rt.Start, End: rt.End})
	}
	for _, rd := range ri.Deps {
		if rd.After == "" || rd.Before == "" {
			return nil, fmt.Errorf("依赖需要 after 和 before 事件")
		}
		in.Deps = append(in.Deps, Dep{After: rd.After, Before: rd.Before, Label: rd.Label})
	}
	if in.SpanBegin == "" || in.SpanEnd == "" {
		return nil, fmt.Errorf("需要 span.begin 和 span.end 指定总耗时起止事件")
	}
	// 校验引用的事件都存在
	check := func(name, ctx string) error {
		if name != "" && !seen[name] {
			return fmt.Errorf("%s 引用了未定义的事件 %q", ctx, name)
		}
		return nil
	}
	for _, t := range in.Tasks {
		for _, e := range []string{t.Request, t.Acquire, t.Start, t.End} {
			if err := check(e, "任务 "+t.Name); err != nil {
				return nil, err
			}
		}
	}
	for _, d := range in.Deps {
		if err := check(d.After, "依赖"); err != nil {
			return nil, err
		}
		if err := check(d.Before, "依赖"); err != nil {
			return nil, err
		}
	}
	if err := check(in.SpanBegin, "span"); err != nil {
		return nil, err
	}
	if err := check(in.SpanEnd, "span"); err != nil {
		return nil, err
	}
	return in, nil
}
