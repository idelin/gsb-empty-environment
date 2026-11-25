// subverify-demo 用四个攻击/合法场景演示验证器，并在拒绝时打印调用点链。
package main

import (
	"fmt"

	"prescreen.local/ps-8dfd82fcf0/subverify"
)

func main() {
	demos := []struct {
		name string
		prog *subverify.Program
	}{
		{
			name: "同一清理段的两个合法调用方",
			prog: &subverify.Program{
				NumSlots: 4, MaxStack: 4,
				Code: []subverify.Instr{
					{Op: subverify.OpJsr, Arg: 4},
					{Op: subverify.OpGoto, Arg: 2},
					{Op: subverify.OpJsr, Arg: 4},
					{Op: subverify.OpReturn},
					{Op: subverify.OpStoreReturn, Arg: 0},
					{Op: subverify.OpRet, Arg: 0},
				},
			},
		},
		{
			name: "外层返回令牌被内层 ret 使用",
			prog: &subverify.Program{
				NumSlots: 4, MaxStack: 4,
				Code: []subverify.Instr{
					{Op: subverify.OpJsr, Arg: 2},
					{Op: subverify.OpReturn},
					{Op: subverify.OpStoreReturn, Arg: 0},
					{Op: subverify.OpJsr, Arg: 5},
					{Op: subverify.OpReturn},
					{Op: subverify.OpStoreReturn, Arg: 1},
					{Op: subverify.OpRet, Arg: 0},
				},
			},
		},
		{
			name: "ret 后跨槽别名被重新加载",
			prog: &subverify.Program{
				NumSlots: 4, MaxStack: 4,
				Code: []subverify.Instr{
					{Op: subverify.OpJsr, Arg: 4},
					{Op: subverify.OpGoto, Arg: 2},
					{Op: subverify.OpLoadReturn, Arg: 0},
					{Op: subverify.OpReturn},
					{Op: subverify.OpStoreReturn, Arg: 0},
					{Op: subverify.OpLoadReturn, Arg: 0},
					{Op: subverify.OpStoreReturn, Arg: 1},
					{Op: subverify.OpRet, Arg: 1},
				},
			},
		},
		{
			name: "循环中再次调用同一子程序（合法普通环）",
			prog: &subverify.Program{
				NumSlots: 4, MaxStack: 4,
				Code: []subverify.Instr{
					{Op: subverify.OpJsr, Arg: 4},
					{Op: subverify.OpGoto, Arg: 2},
					{Op: subverify.OpBranch, Arg: 0},
					{Op: subverify.OpReturn},
					{Op: subverify.OpStoreReturn, Arg: 0},
					{Op: subverify.OpRet, Arg: 0},
				},
			},
		},
	}

	for _, demo := range demos {
		res := subverify.Verify(demo.prog)
		fmt.Printf("场景: %s\n  结论: %s\n", demo.name, res)
		if res.Conclusion == subverify.ConclusionReject {
			fmt.Printf("  拒绝前调用点链: %s\n", res.CallChainText())
		}
		fmt.Println()
	}
}
