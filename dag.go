package policycheck

import (
	"fmt"
)

// Edge 是 DAG 的有向边，可携带跨边审计/通知义务。
type Edge struct {
	To     int      `json:"to"`
	Audit  []string `json:"audit,omitempty"`
	Notify []string `json:"notify,omitempty"`
}

func (e Edge) clone() Edge {
	out := e
	out.Audit = append([]string(nil), e.Audit...)
	out.Notify = append([]string(nil), e.Notify...)
	return out
}

// DAGNode 是测试节点或终止节点。
//
// 测试节点的 True/False 目标下标必须严格大于当前下标，因此整个
// DAG 只能向前跳转且天然无环。终止节点设置 Decision。
type DAGNode struct {
	ID       string    `json:"id,omitempty"`
	Test     *Pred     `json:"test,omitempty"`
	True     Edge      `json:"true,omitempty"`
	False    Edge      `json:"false,omitempty"`
	Decision *Decision `json:"decision,omitempty"`
}

// DAG 是优化后的前向判定 DAG。
type DAG struct {
	Name  string     `json:"name,omitempty"`
	Root  int        `json:"root"`
	Nodes []*DAGNode `json:"nodes"`
}

// TraceStep 记录一次具体求值经过的节点和分支。
type TraceStep struct {
	Node   int    `json:"node"`
	ID     string `json:"id,omitempty"`
	Branch string `json:"branch,omitempty"`
	Value  bool   `json:"value,omitempty"`
	To     int    `json:"to,omitempty"`
}

// ValidateForward 校验根、节点类型以及严格前向边。
func (d *DAG) ValidateForward() error {
	if d == nil {
		return fmt.Errorf("policycheck: nil DAG")
	}
	if len(d.Nodes) == 0 {
		return fmt.Errorf("policycheck: DAG has no nodes")
	}
	if d.Root < 0 || d.Root >= len(d.Nodes) {
		return fmt.Errorf("policycheck: root %d out of range", d.Root)
	}
	for i, n := range d.Nodes {
		if n == nil {
			return fmt.Errorf("policycheck: node %d is nil", i)
		}
		terminal := n.Decision != nil
		test := n.Test != nil
		if terminal == test {
			return fmt.Errorf("policycheck: node %d must be exactly one of test or terminal", i)
		}
		if terminal {
			if !n.Decision.Action.Valid() {
				return fmt.Errorf("policycheck: node %d has invalid action %q", i, n.Decision.Action)
			}
			continue
		}
		if err := n.Test.Validate(); err != nil {
			return fmt.Errorf("policycheck: node %d test: %w", i, err)
		}
		for branch, e := range map[string]Edge{"true": n.True, "false": n.False} {
			if e.To <= i || e.To >= len(d.Nodes) {
				return fmt.Errorf("policycheck: node %d %s edge target %d must be strictly forward and in [1,%d]",
					i, branch, e.To, len(d.Nodes)-1)
			}
		}
	}
	return nil
}

// Evaluate 在具体输入上求值 DAG。
func (d *DAG) Evaluate(in Input, reg Registry) (Decision, []TraceStep, error) {
	if err := d.ValidateForward(); err != nil {
		return Decision{}, nil, err
	}
	if _, ok := NormalizeArch(string(in.Arch)); !ok {
		return Decision{}, nil, fmt.Errorf("policycheck: unsupported input architecture %q", in.Arch)
	}

	var audit, notify []string
	var trace []TraceStep
	idx := d.Root
	guard := 0
	for {
		guard++
		if guard > len(d.Nodes)+1 {
			return Decision{}, trace, fmt.Errorf("policycheck: DAG traversal did not reach terminal")
		}
		if idx < 0 || idx >= len(d.Nodes) {
			return Decision{}, trace, fmt.Errorf("policycheck: edge target %d out of range", idx)
		}
		node := d.Nodes[idx]
		if node.Decision != nil {
			dec := node.Decision.Clone()
			dec.Audit = append(append([]string(nil), audit...), dec.Audit...)
			dec.Notify = append(append([]string(nil), notify...), dec.Notify...)
			trace = append(trace, TraceStep{Node: idx, ID: node.ID, Branch: "terminal"})
			return dec, trace, nil
		}

		v, err := node.Test.Eval(in, reg)
		if err != nil {
			return Decision{}, trace, fmt.Errorf("policycheck: DAG node %d: %w", idx, err)
		}
		edge := node.False
		branch := "false"
		if v {
			edge = node.True
			branch = "true"
		}
		audit = append(audit, edge.Audit...)
		notify = append(notify, edge.Notify...)
		trace = append(trace, TraceStep{Node: idx, ID: node.ID, Branch: branch, Value: v, To: edge.To})
		idx = edge.To
	}
}

// EvaluateSymbolic 在原子谓词真值赋值上求值 DAG。
func (d *DAG) EvaluateSymbolic(values map[string]bool) (Decision, []int, error) {
	if err := d.ValidateForward(); err != nil {
		return Decision{}, nil, err
	}
	var audit, notify []string
	var path []int
	idx := d.Root
	for {
		if idx < 0 || idx >= len(d.Nodes) {
			return Decision{}, path, fmt.Errorf("policycheck: edge target %d out of range", idx)
		}
		path = append(path, idx)
		node := d.Nodes[idx]
		if node.Decision != nil {
			dec := node.Decision.Clone()
			dec.Audit = append(append([]string(nil), audit...), dec.Audit...)
			dec.Notify = append(append([]string(nil), notify...), dec.Notify...)
			return dec, path, nil
		}
		v, err := evalSymbolic(node.Test, values)
		if err != nil {
			return Decision{}, path, fmt.Errorf("policycheck: DAG node %d: %w", idx, err)
		}
		edge := node.False
		if v {
			edge = node.True
		}
		audit = append(audit, edge.Audit...)
		notify = append(notify, edge.Notify...)
		idx = edge.To
		if idx <= path[len(path)-1] {
			return Decision{}, path, fmt.Errorf("policycheck: DAG is not forward at node %d", path[len(path)-1])
		}
	}
}

// Digest 返回整个 DAG 的规范摘要。
func (d *DAG) Digest() Digest { return HashCanonical("dag", d) }
