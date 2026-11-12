// Package resetcheck 供 Go 测试宿主验证被测设备的复位一致性。
//
// 被测设备声称 Reset 已完成，但残留的隐藏计数器可能影响下一次试验。
// 本包在每次复位后构建有限行为树（初态观察集 + 探测序列的响应），
// 并以首次复位为基准逐次比较；发现差异时返回最短探测序列与两个复位历史。
//
// 行为树深度最多为 MaxDepth。探测得到未知响应时分支保留，继续向深层探索。
package resetcheck

import (
	"errors"
	"fmt"
	"strings"
)

// MaxDepth 是行为树的最大探测深度。
const MaxDepth = 6

// Unknown 是未知响应在行为树与复位历史中的标记。
const Unknown = "<unknown>"

// Device 由测试宿主实现，抽象被测设备。
type Device interface {
	// Reset 将设备恢复到其声称的初始状态。
	Reset() error
	// Observe 读取一个初态观察点；ok 为 false 表示取值未知。
	Observe(point string) (value string, ok bool)
	// Probe 发送一个探测输入并返回响应；ok 为 false 表示响应未知，
	// 对应分支仍会保留并继续探索。
	Probe(input string) (response string, ok bool)
}

// Checkpointer 是可选接口。行为树存在分支时，探索需要在同一复位会话内
// 回到已访问状态，设备（或宿主代理）须支持快照与恢复。
type Checkpointer interface {
	// Snapshot 捕获当前会话状态。
	Snapshot() (state any, err error)
	// Restore 恢复到此前 Snapshot 捕获的状态。
	Restore(state any) error
}

// Config 描述一次复位一致性检查。
type Config struct {
	// Observations 是初态观察点集合，每次复位后立即求值。
	Observations []string
	// Sequences 是探测序列集合，其前缀闭包构成行为树；
	// 单条序列长度不得超过 MaxDepth。
	Sequences [][]string
	// Resets 是复位次数，至少为 2。
	Resets int
}

// Observation 是一次初态观察的结果。
type Observation struct {
	Point string
	Value string // Known 为 false 时为 Unknown
	Known bool
}

// Step 是复位历史中的一步探测。
type Step struct {
	Probe    string
	Response string // Known 为 false 时为 Unknown
	Known    bool
}

// History 是一次复位沿分歧路径的完整历史：初态观察加探测步骤。
type History struct {
	Reset        int
	Observations []Observation
	Steps        []Step
}

// Result 描述两次复位之间发现的差异。
type Result struct {
	// Path 是暴露差异的最短探测序列；为空表示初态观察集已不同。
	Path []string
	// Reason 是可读的分歧原因。
	Reason string
	// A 与 B 是两个复位历史，A 来自较早的复位。
	A, B History
}

// String 返回分歧的可读摘要。
func (r *Result) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "复位 %d 与复位 %d 行为不一致：%s\n", r.A.Reset, r.B.Reset, r.Reason)
	fmt.Fprintf(&b, "最短探测序列: %s\n", formatPath(r.Path))
	fmt.Fprintf(&b, "复位 %d 历史: %s\n", r.A.Reset, formatHistory(r.A))
	fmt.Fprintf(&b, "复位 %d 历史: %s", r.B.Reset, formatHistory(r.B))
	return b.String()
}

func formatPath(path []string) string {
	if len(path) == 0 {
		return "(空，初态观察即不同)"
	}
	return strings.Join(path, " -> ")
}

func formatHistory(h History) string {
	var parts []string
	for _, o := range h.Observations {
		parts = append(parts, fmt.Sprintf("%s=%s", o.Point, o.Value))
	}
	for _, s := range h.Steps {
		parts = append(parts, fmt.Sprintf("%s=>%s", s.Probe, s.Response))
	}
	return strings.Join(parts, "  ")
}

// Check 执行复位一致性检查。每次复位后构建有限行为树并与首次复位比较；
// 发现差异时返回描述最短探测序列与两个复位历史的 Result，否则返回 nil。
func Check(dev Device, cfg Config) (*Result, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if err := dev.Reset(); err != nil {
		return nil, fmt.Errorf("resetcheck: 复位 0 失败: %w", err)
	}
	ref, err := buildTree(dev, cfg)
	if err != nil {
		return nil, err
	}
	for run := 1; run < cfg.Resets; run++ {
		if err := dev.Reset(); err != nil {
			return nil, fmt.Errorf("resetcheck: 复位 %d 失败: %w", run, err)
		}
		cur, err := buildTree(dev, cfg)
		if err != nil {
			return nil, err
		}
		if res := compareTrees(ref, cur, 0, run); res != nil {
			return res, nil
		}
	}
	return nil, nil
}

func (c Config) validate() error {
	if c.Resets < 2 {
		return fmt.Errorf("resetcheck: Resets=%d，至少需要 2 次复位", c.Resets)
	}
	for _, seq := range c.Sequences {
		if len(seq) > MaxDepth {
			return fmt.Errorf("resetcheck: 探测序列 %q 长度为 %d，超过最大深度 %d", seq, len(seq), MaxDepth)
		}
	}
	return nil
}

// edge 是行为树中的一条探测边。
type edge struct {
	input    string
	response string
	known    bool
	child    *node
}

// node 是行为树节点；仅根节点持有初态观察集。
type node struct {
	obs      []Observation
	children []*edge
}

func findChild(n *node, input string) *edge {
	for _, e := range n.children {
		if e.input == input {
			return e
		}
	}
	return nil
}

// buildTree 在一次复位会话内探索有限行为树。
func buildTree(dev Device, cfg Config) (*node, error) {
	root := &node{}
	for _, seq := range cfg.Sequences {
		cur := root
		for _, input := range seq {
			e := findChild(cur, input)
			if e == nil {
				e = &edge{input: input, child: &node{}}
				cur.children = append(cur.children, e)
			}
			cur = e.child
		}
	}
	cp, _ := dev.(Checkpointer)
	root.obs = observeAll(dev, cfg.Observations)
	if err := walk(dev, cp, root, 0); err != nil {
		return nil, err
	}
	return root, nil
}

func observeAll(dev Device, points []string) []Observation {
	obs := make([]Observation, 0, len(points))
	for _, p := range points {
		value, ok := dev.Observe(p)
		if !ok {
			value = Unknown
		}
		obs = append(obs, Observation{Point: p, Value: value, Known: ok})
	}
	return obs
}

var errNoCheckpoint = errors.New("resetcheck: 行为树存在分支，设备需实现 Checkpointer 以支持会话内回溯")

// walk 深度优先探索子树；未知响应的分支保留并继续深入。
func walk(dev Device, cp Checkpointer, n *node, depth int) error {
	if depth > MaxDepth {
		return fmt.Errorf("resetcheck: 探测深度超过 %d", MaxDepth)
	}
	var snap any
	if len(n.children) > 1 {
		if cp == nil {
			return errNoCheckpoint
		}
		var err error
		snap, err = cp.Snapshot()
		if err != nil {
			return fmt.Errorf("resetcheck: 快照失败: %w", err)
		}
	}
	for i, e := range n.children {
		resp, ok := dev.Probe(e.input)
		if !ok {
			resp = Unknown
		}
		e.response, e.known = resp, ok
		if err := walk(dev, cp, e.child, depth+1); err != nil {
			return err
		}
		if i < len(n.children)-1 {
			if err := cp.Restore(snap); err != nil {
				return fmt.Errorf("resetcheck: 恢复失败: %w", err)
			}
		}
	}
	return nil
}

// compareTrees 按广度优先比较两棵行为树，保证返回最短的分歧路径。
func compareTrees(a, b *node, resetA, resetB int) *Result {
	if reason := compareObservations(a.obs, b.obs); reason != "" {
		return &Result{
			Reason: reason,
			A:      history(a, nil, resetA),
			B:      history(b, nil, resetB),
		}
	}
	type item struct {
		na, nb *node
		path   []string
	}
	queue := []item{{a, b, nil}}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		for i, ea := range it.na.children {
			eb := it.nb.children[i]
			path := make([]string, 0, len(it.path)+1)
			path = append(path, it.path...)
			path = append(path, ea.input)
			var reason string
			switch {
			case ea.known != eb.known:
				reason = fmt.Sprintf("探测 %q 的响应可见性不同（%s 对 %s）",
					ea.input, responseText(ea), responseText(eb))
			case ea.known && ea.response != eb.response:
				reason = fmt.Sprintf("探测 %q 的响应不同（%q 对 %q）",
					ea.input, ea.response, eb.response)
			}
			if reason != "" {
				return &Result{
					Path:   path,
					Reason: reason,
					A:      history(a, path, resetA),
					B:      history(b, path, resetB),
				}
			}
			queue = append(queue, item{ea.child, eb.child, path})
		}
	}
	return nil
}

func responseText(e *edge) string {
	if !e.known {
		return Unknown
	}
	return fmt.Sprintf("%q", e.response)
}

func compareObservations(a, b []Observation) string {
	for i := range a {
		switch {
		case a[i].Known != b[i].Known:
			return fmt.Sprintf("初态观察 %q 的可见性不同（%s 对 %s）",
				a[i].Point, a[i].Value, b[i].Value)
		case a[i].Known && a[i].Value != b[i].Value:
			return fmt.Sprintf("初态观察 %q 的取值不同（%q 对 %q）",
				a[i].Point, a[i].Value, b[i].Value)
		}
	}
	return ""
}

// history 沿分歧路径提取一次复位的历史。
func history(root *node, path []string, reset int) History {
	h := History{Reset: reset, Observations: root.obs}
	cur := root
	for _, input := range path {
		e := findChild(cur, input)
		h.Steps = append(h.Steps, Step{Probe: e.input, Response: e.response, Known: e.known})
		cur = e.child
	}
	return h
}
