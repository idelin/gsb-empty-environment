package subverify

import "testing"

// dead 仅用于填充不可达位置，使指令数组的下标与注释中的 pc 对齐。
var dead = Instr{Op: OpReturn}

func TestTwoLegalCallersShareCleanup(t *testing.T) {
	prog := &Program{
		NumSlots: 4,
		MaxStack: 4,
		Code: []Instr{
			0: {Op: OpJsr, Arg: 4}, // 调用方 A
			1: {Op: OpGoto, Arg: 2},
			2: {Op: OpJsr, Arg: 4}, // 调用方 B：共享同一清理段
			3: {Op: OpReturn},
			4: {Op: OpStoreReturn, Arg: 0},
			5: {Op: OpRet, Arg: 0},
		},
	}
	res := Verify(prog)
	if res.Conclusion != ConclusionAccept {
		t.Fatalf("两个合法调用方应通过，实际: %v", res)
	}
}

func TestOuterTokenUsedByInnerRet(t *testing.T) {
	prog := &Program{
		NumSlots: 4,
		MaxStack: 4,
		Code: []Instr{
			0: {Op: OpJsr, Arg: 2},
			1: {Op: OpReturn},
			2: {Op: OpStoreReturn, Arg: 0}, // 外层令牌存入槽 0
			3: {Op: OpJsr, Arg: 5},         // 内层调用
			4: dead,
			5: {Op: OpStoreReturn, Arg: 1}, // 内层令牌存入槽 1
			6: {Op: OpRet, Arg: 0},         // 恶意：用外层令牌驱动内层 ret
		},
	}
	res := Verify(prog)
	if res.Conclusion != ConclusionReject {
		t.Fatalf("外层令牌被内层 ret 使用必须拒绝，实际: %v", res)
	}
	wantChain := []int{0, 3}
	if !equalInts(res.CallChain, wantChain) {
		t.Fatalf("拒绝前调用点链 = %v，期望 %v", res.CallChain, wantChain)
	}
	if res.AtPC != 6 {
		t.Fatalf("拒绝位置 = %d，期望 6", res.AtPC)
	}
	t.Logf("已拒绝: %s", res)
}

func TestCrossSlotAliasesDieAfterRet(t *testing.T) {
	prog := &Program{
		NumSlots: 4,
		MaxStack: 4,
		Code: []Instr{
			0: {Op: OpJsr, Arg: 4},
			1: {Op: OpGoto, Arg: 2},
			2: {Op: OpLoadReturn, Arg: 0}, // ret 后加载槽 0 的失效别名
			3: {Op: OpReturn},
			4: {Op: OpStoreReturn, Arg: 0},
			5: {Op: OpLoadReturn, Arg: 0},
			6: {Op: OpStoreReturn, Arg: 1}, // 派生拷贝进入槽 1，栈恢复到 jsr 快照深度
			7: {Op: OpRet, Arg: 1},
			8: dead,
		},
	}
	res := Verify(prog)
	if res.Conclusion != ConclusionReject {
		t.Fatalf("ret 后加载失效令牌别名必须拒绝，实际: %v", res)
	}
	if res.AtPC != 2 {
		t.Fatalf("拒绝位置 = %d，期望 2", res.AtPC)
	}
	if len(res.CallChain) != 0 {
		t.Fatalf("拒绝发生在根上下文，调用链应为空，实际 %v", res.CallChain)
	}
	t.Logf("已拒绝: %s", res)
}

func TestLoopReentersCleanupAcrossInvocations(t *testing.T) {
	prog := &Program{
		NumSlots: 4,
		MaxStack: 4,
		Code: []Instr{
			0: {Op: OpJsr, Arg: 4}, // 循环内反复调用同一清理段
			1: {Op: OpGoto, Arg: 2},
			2: {Op: OpBranch, Arg: 0}, // 非确定分支：回到 0 再次调用，或落到 3
			3: {Op: OpReturn},
			4: {Op: OpStoreReturn, Arg: 0},
			5: {Op: OpRet, Arg: 0},
		},
	}
	res := Verify(prog)
	if res.Conclusion != ConclusionAccept {
		t.Fatalf("普通控制流环中再次调用同一子程序应通过（非活动调用，不算递归），实际: %v", res)
	}
}

func TestStaleTokenReusedByLaterInvocation(t *testing.T) {
	prog := &Program{
		NumSlots: 4,
		MaxStack: 4,
		Code: []Instr{
			0: {Op: OpJsr, Arg: 3}, // 第一次调用：槽 0 留下令牌
			1: {Op: OpJsr, Arg: 6}, // 第二次调用：ret 复用槽 0 的陈旧令牌
			2: {Op: OpReturn},
			3: {Op: OpStoreReturn, Arg: 0},
			4: {Op: OpRet, Arg: 0},
			5: dead,
			6: {Op: OpRet, Arg: 0}, // 槽 0 已是失效令牌
		},
	}
	res := Verify(prog)
	if res.Conclusion != ConclusionReject {
		t.Fatalf("跨多次调用复用外层槽中的返回令牌必须拒绝，实际: %v", res)
	}
	if !equalInts(res.CallChain, []int{1}) {
		t.Fatalf("拒绝前调用点链 = %v，期望 [1]", res.CallChain)
	}
	t.Logf("已拒绝: %s", res)
}

func TestReturnPreconditions(t *testing.T) {
	insideSub := &Program{
		NumSlots: 2, MaxStack: 2,
		Code: []Instr{
			0: {Op: OpJsr, Arg: 2},
			1: {Op: OpReturn},
			2: {Op: OpReturn}, // 活动调用未清空
		},
	}
	if res := Verify(insideSub); res.Conclusion != ConclusionReject ||
		!equalInts(res.CallChain, []int{0}) {
		t.Fatalf("子程序内 return 应携带调用链 [0] 拒绝，实际: %v", res)
	}

	nonEmptyStack := &Program{
		NumSlots: 2, MaxStack: 2,
		Code: []Instr{
			0: {Op: OpPushInt},
			1: {Op: OpReturn}, // 操作数栈非空
		},
	}
	if res := Verify(nonEmptyStack); res.Conclusion != ConclusionReject {
		t.Fatalf("非空栈 return 应拒绝，实际: %v", res)
	}
}

func TestRecursionRejected(t *testing.T) {
	prog := &Program{
		NumSlots: 2, MaxStack: 2,
		Code: []Instr{
			0: {Op: OpJsr, Arg: 2},
			1: {Op: OpReturn},
			2: {Op: OpJsr, Arg: 2}, // 活动调用中再次进入同一子程序
		},
	}
	res := Verify(prog)
	if res.Conclusion != ConclusionReject {
		t.Fatalf("递归进入活动子程序必须拒绝，实际: %v", res)
	}
	if !equalInts(res.CallChain, []int{0}) {
		t.Fatalf("调用链 = %v，期望 [0]", res.CallChain)
	}
}

func TestIntTokenNotConvertible(t *testing.T) {
	cases := map[string]*Program{
		"storeInt 吞掉返回令牌": {
			NumSlots: 2, MaxStack: 2,
			Code: []Instr{
				0: {Op: OpJsr, Arg: 2},
				1: {Op: OpReturn},
				2: {Op: OpStoreInt, Arg: 0},
			},
		},
		"loadInt 读取令牌槽": {
			NumSlots: 2, MaxStack: 2,
			Code: []Instr{
				0: {Op: OpJsr, Arg: 2},
				1: {Op: OpReturn},
				2: {Op: OpStoreReturn, Arg: 0},
				3: {Op: OpLoadInt, Arg: 0},
			},
		},
		"storeReturn 吞掉整数": {
			NumSlots: 2, MaxStack: 2,
			Code: []Instr{
				0: {Op: OpPushInt},
				1: {Op: OpStoreReturn, Arg: 0},
			},
		},
	}
	for name, prog := range cases {
		t.Run(name, func(t *testing.T) {
			if res := Verify(prog); res.Conclusion != ConclusionReject {
				t.Fatalf("%s 应拒绝，实际: %v", name, res)
			}
		})
	}
}

func TestBudgetExhaustionIsUnknownNotAccept(t *testing.T) {
	prog := &Program{
		NumSlots: 2, MaxStack: 2,
		Code: []Instr{
			0: {Op: OpPushInt},
			1: {Op: OpPop},
			2: {Op: OpGoto, Arg: 0}, // 普通控制流环
		},
	}
	res := VerifyWithBudget(prog, 1)
	if res.Conclusion != ConclusionUnknown {
		t.Fatalf("预算耗尽应给未知结论，实际: %v", res)
	}
	if Verify(prog).Conclusion != ConclusionAccept {
		t.Fatalf("预算充足时同一程序应收敛并通过")
	}
}

func TestMergeIncompatibility(t *testing.T) {
	stackMismatch := &Program{
		NumSlots: 2, MaxStack: 4,
		Code: []Instr{
			0: {Op: OpBranch, Arg: 3},
			1: {Op: OpPushInt},
			2: {Op: OpGoto, Arg: 5},
			3: {Op: OpPushInt},
			4: {Op: OpPushInt},
			5: {Op: OpPop}, // 两条路径栈深 1 与 2，合流拒绝
			6: {Op: OpReturn},
		},
	}
	if res := Verify(stackMismatch); res.Conclusion != ConclusionReject || res.AtPC != 5 {
		t.Fatalf("栈不相容合流应在 pc5 拒绝，实际: %v", res)
	}

	localsMismatch := &Program{
		NumSlots: 2, MaxStack: 4,
		Code: []Instr{
			0: {Op: OpBranch, Arg: 4},
			1: {Op: OpPushInt},
			2: {Op: OpStoreInt, Arg: 0},
			3: {Op: OpGoto, Arg: 6},
			4: {Op: OpJsr, Arg: 8},
			5: {Op: OpGoto, Arg: 6},
			6: {Op: OpLoadInt, Arg: 0}, // 槽 0 合流为 Int/失效令牌 -> 不可用
			7: {Op: OpReturn},
			8: {Op: OpStoreReturn, Arg: 0},
			9: {Op: OpRet, Arg: 0},
		},
	}
	res := Verify(localsMismatch)
	if res.Conclusion != ConclusionReject || res.AtPC != 6 {
		t.Fatalf("不相容局部槽应变不可用并在加载时于 pc6 拒绝，实际: %v", res)
	}
}

func TestNestingLimit(t *testing.T) {
	if res := Verify(nestedProgram(4)); res.Conclusion != ConclusionAccept {
		t.Fatalf("4 层嵌套子程序应通过，实际: %v", res)
	}
	res := Verify(nestedProgram(5))
	if res.Conclusion != ConclusionReject {
		t.Fatalf("5 层嵌套必须拒绝，实际: %v", res)
	}
}

func nestedProgram(depth int) *Program {
	code := []Instr{
		{Op: OpJsr, Arg: 2},
		{Op: OpReturn},
	}
	for level := 0; level < depth; level++ {
		code = append(code, Instr{Op: OpStoreReturn, Arg: level})
		if level == depth-1 {
			code = append(code, Instr{Op: OpRet, Arg: level})
			continue
		}
		nextEntry := len(code) + 2
		code = append(code,
			Instr{Op: OpJsr, Arg: nextEntry},
			Instr{Op: OpRet, Arg: level},
		)
	}
	return &Program{Code: code, NumSlots: 8, MaxStack: 8}
}

func TestResourceLimitsAndCallChainText(t *testing.T) {
	if res := Verify(&Program{Code: []Instr{{Op: OpReturn}}, NumSlots: 33, MaxStack: 8}); res.Conclusion != ConclusionReject {
		t.Fatalf("槽数 33 应拒绝，实际: %v", res)
	}
	if res := Verify(&Program{Code: []Instr{{Op: OpReturn}}, NumSlots: 8, MaxStack: 33}); res.Conclusion != ConclusionReject {
		t.Fatalf("栈深 33 应拒绝，实际: %v", res)
	}
	if res := Verify(&Program{Code: []Instr{{Op: OpGoto, Arg: 9}}, NumSlots: 2, MaxStack: 2}); res.Conclusion != ConclusionReject {
		t.Fatalf("越界跳转应拒绝，实际: %v", res)
	}
	r := Result{CallChain: []int{0, 3}}
	if r.CallChainText() != "jsr@0 -> jsr@3" {
		t.Fatalf("调用链渲染错误: %q", r.CallChainText())
	}
	if (Result{}).CallChainText() != "<root>" {
		t.Fatalf("根上下文渲染错误: %q", (Result{}).CallChainText())
	}
}

func TestBoundsAtLimit32(t *testing.T) {
	// 32 个槽、32 深栈处于允许上限：先压满再逐一存入槽，最后清空并返回。
	code := make([]Instr, 0, 80)
	for i := 0; i < 32; i++ {
		code = append(code, Instr{Op: OpPushInt})
	}
	for i := 31; i >= 0; i-- {
		code = append(code, Instr{Op: OpStoreInt, Arg: i})
	}
	code = append(code, Instr{Op: OpReturn})
	if res := Verify(&Program{Code: code, NumSlots: 32, MaxStack: 32}); res.Conclusion != ConclusionAccept {
		t.Fatalf("上限 32 的合法程序应通过，实际: %v", res)
	}

	// 再压一个整数即突破栈深。
	overflow := append(append([]Instr(nil), code[:len(code)-1]...),
		Instr{Op: OpPushInt}, Instr{Op: OpReturn})
	if res := Verify(&Program{Code: overflow, NumSlots: 32, MaxStack: 32}); res.Conclusion != ConclusionReject {
		t.Fatalf("超过 32 栈深应拒绝，实际: %v", res)
	}

	// 槽号越界同样拒绝。
	badSlot := &Program{
		NumSlots: 32, MaxStack: 32,
		Code: []Instr{{Op: OpPushInt}, {Op: OpStoreInt, Arg: 32}, {Op: OpReturn}},
	}
	if res := Verify(badSlot); res.Conclusion != ConclusionReject {
		t.Fatalf("槽号 32 越界应拒绝，实际: %v", res)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
