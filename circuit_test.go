package netsec

import "testing"

func TestNewCircuitValidation(t *testing.T) {
	cases := []struct {
		name  string
		nodes []Node
	}{
		{"空电路", nil},
		{"缺少输出", []Node{{Kind: KindInput}}},
		{"输出不唯一", []Node{
			{Kind: KindInput},
			{Kind: KindOutput, Args: []int{0}},
			{Kind: KindOutput, Args: []int{0}},
		}},
		{"操作数非前驱", []Node{
			{Kind: KindInput},
			{Kind: KindNot, Args: []int{1}},
			{Kind: KindOutput, Args: []int{0}},
		}},
		{"操作数个数错误", []Node{
			{Kind: KindInput},
			{Kind: KindAnd, Args: []int{0}},
			{Kind: KindOutput, Args: []int{0}},
		}},
		{"类型非法", []Node{
			{Kind: NodeKind(99)},
			{Kind: KindOutput, Args: []int{0}},
		}},
	}
	for _, tc := range cases {
		if _, err := NewCircuit(tc.nodes); err == nil {
			t.Errorf("%s: 期望报错", tc.name)
		}
	}
	// 节点数超限。
	tooMany := make([]Node, MaxNodes+1)
	tooMany[0] = Node{Kind: KindInput}
	for i := 1; i < len(tooMany)-1; i++ {
		tooMany[i] = Node{Kind: KindNot, Args: []int{i - 1}}
	}
	tooMany[len(tooMany)-1] = Node{Kind: KindOutput, Args: []int{len(tooMany) - 2}}
	if _, err := NewCircuit(tooMany); err == nil {
		t.Error("节点数超限: 期望报错")
	}
	// 输入位数超限。
	var tooManyIn []Node
	for i := 0; i <= MaxInputs; i++ {
		tooManyIn = append(tooManyIn, Node{Kind: KindInput})
	}
	tooManyIn = append(tooManyIn, Node{Kind: KindOutput, Args: []int{0}})
	if _, err := NewCircuit(tooManyIn); err == nil {
		t.Error("输入位数超限: 期望报错")
	}
}

func TestEvalAndFault(t *testing.T) {
	// a, b 输入；n2 = a AND b；n3 = NOT n2；out = n3。
	c, err := NewCircuit([]Node{
		{Kind: KindInput},
		{Kind: KindInput},
		{Kind: KindAnd, Args: []int{0, 1}},
		{Kind: KindNot, Args: []int{2}},
		{Kind: KindOutput, Args: []int{3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []bool{true, true, true, false} // 输入 0..3 的期望输出
	for j := 0; j < 4; j++ {
		if got := c.Output(c.Eval(uint32(j))); got != want[j] {
			t.Errorf("输入 %d: 正常输出 = %v, 期望 %v", j, got, want[j])
		}
	}
	// n2 卡 0：输出恒 1。
	f := Fault{Node: 2, StuckAt: false}
	for j := 0; j < 4; j++ {
		states := c.EvalWithFault(uint32(j), f)
		if states[2] != false {
			t.Errorf("输入 %d: 故障节点状态应为 false", j)
		}
		if !c.Output(states) {
			t.Errorf("输入 %d: n2 卡 0 后输出应为 1", j)
		}
	}
	// 位并行与标量仿真逐输入一致（含故障）。
	for _, f := range append([]Fault{{Node: -1}}, c.AllFaults()...) {
		var tab [][]uint64
		if f.Node < 0 {
			tab = c.simulate(nil)
		} else {
			f := f
			tab = c.simulate(&f)
		}
		for j := 0; j < c.InputSpace(); j++ {
			var states []bool
			if f.Node < 0 {
				states = c.Eval(uint32(j))
			} else {
				states = c.EvalWithFault(uint32(j), f)
			}
			for i := range states {
				if bitOf(tab[i], uint32(j)) != states[i] {
					t.Fatalf("故障 %v 输入 %d 节点 %d: 位并行与标量不一致", f, j, i)
				}
			}
		}
	}
}
