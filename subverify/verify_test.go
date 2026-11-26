package subverify

import (
	"strings"
	"testing"
)

func jsr(t int) Instr     { return Instr{Op: JSR, Target: t} }
func ret(s int) Instr     { return Instr{Op: RET, Slot: s} }
func storeR(s int) Instr  { return Instr{Op: StoreReturn, Slot: s} }
func loadR(s int) Instr   { return Instr{Op: LoadReturn, Slot: s} }
func pushInt() Instr      { return Instr{Op: PushInt} }
func storeI(s int) Instr  { return Instr{Op: StoreInt, Slot: s} }
func loadI(s int) Instr   { return Instr{Op: LoadInt, Slot: s} }
func pop() Instr          { return Instr{Op: Pop} }
func gotoPC(t int) Instr  { return Instr{Op: Goto, Target: t} }
func branch(t int) Instr  { return Instr{Op: Branch, Target: t} }
func methodReturn() Instr { return Instr{Op: Return} }

func assertStatus(t *testing.T, prog []Instr, slots int, cfg Config, want Status) *Result {
	t.Helper()
	res := Verify(prog, slots, cfg)
	if res.Status != want {
		t.Fatalf("状态 = %s, 期望 %s; 详情: %s", res.Status, want, res)
	}
	return res
}

// 同一清理子程序被两个合法调用方共享：两个调用点拥有独立上下文与令牌。
func TestTwoCallersSharedCleanup(t *testing.T) {
	prog := []Instr{
		jsr(8),         // 0 调用方 A
		pushInt(),      // 1
		pop(),          // 2
		methodReturn(), // 3
		jsr(8),         // 4 调用方 B
		pushInt(),      // 5
		pop(),          // 6
		methodReturn(), // 7
		storeR(0),      // 8 共享清理段入口
		loadR(0),       // 9
		ret(0),         // 10
	}
	res := assertStatus(t, prog, 4, Config{}, Loadable)
	if len(res.CallChain) != 0 {
		t.Fatalf("可加载结果不应携带调用点链: %v", res.CallChain)
	}
}

// 外层调用的返回令牌被内层子程序的 ret 使用：必须拒绝并输出完整调用点链。
func TestOuterTokenUsedByInnerRet(t *testing.T) {
	prog := []Instr{
		jsr(4),         // 0 进入子程序 A
		pushInt(),      // 1 A 的返回点
		pop(),          // 2
		methodReturn(), // 3
		storeR(0),      // 4 A：把 A 的令牌存入槽 0
		jsr(8),         // 5 进入内层子程序 B
		pushInt(),      // 6 B 的返回点
		pop(),          // 7
		storeR(1),      // 8 B：保存 B 自己的令牌
		ret(0),         // 9 错用槽 0 中外层 A 的令牌
	}
	res := assertStatus(t, prog, 4, Config{}, Rejected)
	if len(res.CallChain) != 2 {
		t.Fatalf("调用点链长度 = %d，期望 2: %s", len(res.CallChain), res)
	}
	if res.CallChain[0].JSRIndex != 0 || res.CallChain[0].Target != 4 {
		t.Fatalf("链首应为 jsr@0 -> 4，实际 %v", res.CallChain[0])
	}
	if res.CallChain[1].JSRIndex != 5 || res.CallChain[1].Target != 8 {
		t.Fatalf("链尾应为 jsr@5 -> 8，实际 %v", res.CallChain[1])
	}
	if !strings.Contains(res.String(), "jsr@0 -> 4") || !strings.Contains(res.String(), "jsr@5 -> 8") {
		t.Fatalf("字符串输出缺少调用点链: %s", res)
	}
}

// ret 之后令牌的跨槽别名全部失效，再次加载即拒绝。
func TestAliasDeadAfterRet(t *testing.T) {
	prog := []Instr{
		jsr(5),         // 0
		loadR(1),       // 1 槽 1 中是已失效的派生别名
		pop(),          // 2
		methodReturn(), // 3
		methodReturn(), // 4 占位不可达
		storeR(0),      // 5 子程序：令牌存槽 0
		loadR(0),       // 6
		storeR(1),      // 7 派生拷贝到槽 1
		loadR(1),       // 8
		pop(),          // 9 保持 ret 时栈深与入口一致（槽 0/1 中别名仍保留）
		ret(0),         // 10 返回，槽 0/1 中的派生拷贝同时失效
	}
	res := assertStatus(t, prog, 4, Config{}, Rejected)
	if len(res.CallChain) != 1 || res.CallChain[0].JSRIndex != 0 {
		t.Fatalf("拒绝链应只含 jsr@0，实际 %v", res.CallChain)
	}
}

// 普通控制流环中反复调用同一清理子程序：上下文按调用点复用，可收敛、可加载。
func TestLoopRecall(t *testing.T) {
	prog := []Instr{
		jsr(7),         // 0
		branch(0),      // 1 非确定分支：回到 0 形成循环，或落到 2
		methodReturn(), // 2
		methodReturn(), // 3
		methodReturn(), // 4
		methodReturn(), // 5
		methodReturn(), // 6
		storeR(0),      // 7 清理段
		loadR(0),       // 8
		ret(0),         // 9
	}
	assertStatus(t, prog, 4, Config{}, Loadable)
}

// 返回令牌留在外层槽，跨越两次调用后再加载：令牌已失效，拒绝。
func TestTokenRetainedAcrossCalls(t *testing.T) {
	prog := []Instr{
		jsr(6),         // 0 第一次调用
		jsr(6),         // 1 第二次调用
		loadR(0),       // 2 试图复用历史令牌
		pop(),          // 3
		methodReturn(), // 4
		methodReturn(), // 5
		storeR(0),      // 6
		loadR(0),       // 7
		ret(0),         // 8
	}
	assertStatus(t, prog, 4, Config{}, Rejected)
}

// 合流时局部槽 int 与返回令牌不相容：槽变为不可用；不触碰即可加载，触碰即拒绝。
func TestMergeLocalsIncompatible(t *testing.T) {
	build := func(useSlot bool) []Instr {
		prog := []Instr{
			branch(4),      // 0 路径分叉：fall-through 走令牌路径，跳转走整数路径
			jsr(10),        // 1 令牌路径，返回到 2
			gotoPC(7),      // 2
			pushInt(),      // 3 占位保持两侧栈深一致
			pushInt(),      // 4 整数路径
			storeI(0),      // 5 槽 0 = 整数
			gotoPC(7),      // 6
			loadI(0),       // 7 合流点：槽 0 不相容 -> 不可用
			pop(),          // 8
			methodReturn(), // 9
			storeR(0),      // 10 子程序：槽 0 = 返回令牌（与整数路径合流）
			loadR(0),       // 11 恢复入口栈深
			ret(0),         // 12
			methodReturn(), // 13 占位
		}
		if !useSlot {
			prog[7] = methodReturn()
			prog[8] = methodReturn()
		}
		return prog
	}
	assertStatus(t, build(false), 4, Config{}, Loadable)
	assertStatus(t, build(true), 4, Config{}, Rejected)
}

// 合流时操作数栈深度不相容：直接拒绝，不做退化。
func TestMergeStackIncompatible(t *testing.T) {
	prog := []Instr{
		branch(3),      // 0
		pushInt(),      // 1
		pushInt(),      // 2 落到 3 时栈深 2
		pushInt(),      // 3 跳转到达时栈深 1
		pop(),          // 4
		pop(),          // 5
		pop(),          // 6
		methodReturn(), // 7
	}
	assertStatus(t, prog, 4, Config{}, Rejected)
}

// 活动调用中再次进入同一子程序属于递归：拒绝，链上两个调用点目标相同。
func TestRecursionRejected(t *testing.T) {
	prog := []Instr{
		jsr(3),         // 0
		methodReturn(), // 1
		methodReturn(), // 2
		storeR(0),      // 3 子程序入口
		jsr(3),         // 4 递归进入活动中的自身
		loadR(0),       // 5
		ret(0),         // 6
	}
	res := assertStatus(t, prog, 4, Config{}, Rejected)
	if len(res.CallChain) != 1 {
		t.Fatalf("递归拒绝链长度 = %d，期望 1: %s", len(res.CallChain), res)
	}
	if res.CallChain[0].Target != 3 || res.CallChain[0].JSRIndex != 0 {
		t.Fatalf("链应指向 jsr@0 -> 3: %v", res.CallChain)
	}
}

// 嵌套超过 4 层：第 5 层 jsr 被拒绝，链长 4。
func TestNestingDepthLimit(t *testing.T) {
	prog := []Instr{
		jsr(3),         // 0 深度 1
		methodReturn(), // 1
		methodReturn(), // 2
		jsr(6),         // 3 深度 2
		ret(0),         // 4
		methodReturn(), // 5
		jsr(9),         // 6 深度 3
		ret(0),         // 7
		methodReturn(), // 8
		jsr(12),        // 9 深度 4
		ret(0),         // 10
		methodReturn(), // 11
		jsr(15),        // 12 尝试深度 5 -> 拒绝
		ret(0),         // 13
		methodReturn(), // 14
		ret(0),         // 15
	}
	res := assertStatus(t, prog, 4, Config{}, Rejected)
	if len(res.CallChain) != 4 {
		t.Fatalf("深度拒绝链长度 = %d，期望 4: %s", len(res.CallChain), res)
	}
}

// 方法 return 时活动调用栈或操作数栈非空。
func TestReturnPreconditions(t *testing.T) {
	assertStatus(t, []Instr{pushInt(), methodReturn()}, 1, Config{}, Rejected)

	prog := []Instr{
		jsr(2),         // 0
		methodReturn(), // 1
		methodReturn(), // 2 子程序内部直接方法 return
	}
	res := assertStatus(t, prog, 1, Config{}, Rejected)
	if len(res.CallChain) != 1 {
		t.Fatalf("子程序内 return 应带 1 层调用链: %v", res.CallChain)
	}
}

// 整数与返回令牌不可相互转换、不可混用。
func TestIntTokenNonConvertible(t *testing.T) {
	assertStatus(t, []Instr{pushInt(), storeR(0), methodReturn()}, 1, Config{}, Rejected)

	prog1 := []Instr{
		jsr(4),
		methodReturn(),
		methodReturn(),
		methodReturn(),
		storeI(0), // 子程序对返回令牌执行整数 store
		ret(0),
	}
	assertStatus(t, prog1, 1, Config{}, Rejected)

	prog2 := []Instr{
		pushInt(),
		storeI(0),
		ret(0), // 对整数槽执行 ret
	}
	assertStatus(t, prog2, 1, Config{}, Rejected)
}

// 预算耗尽给出 Unknown 而不是 Loadable/Rejected。
func TestBudgetExhaustedUnknown(t *testing.T) {
	prog := []Instr{
		branch(0),
		methodReturn(),
	}
	res := assertStatus(t, prog, 1, Config{MaxBudget: 1}, Unknown)
	if res.Reason == "" {
		t.Fatal("Unknown 结论应附带原因")
	}
}

// 结构性错误在根上下文拒绝，且不产生调用点链。
func TestStructuralRejections(t *testing.T) {
	cases := [][]Instr{
		{},
		{jsr(9), methodReturn()},
		{gotoPC(-1)},
		{Instr{Op: 99}},
		{pushInt(), storeI(2)},
	}
	for i, prog := range cases {
		res := Verify(prog, 2, Config{})
		if res.Status != Rejected || len(res.CallChain) != 0 {
			t.Fatalf("用例 %d 期望根上下文拒绝，实际 %s", i, res)
		}
	}
	if res := Verify([]Instr{methodReturn()}, MaxSlots+1, Config{}); res.Status != Rejected {
		t.Fatalf("槽数超上限应拒绝，实际 %s", res)
	}
}

// 栈深 32 合法，33 拒绝。
func TestStackDepthLimit(t *testing.T) {
	ok := make([]Instr, 0, 2*MaxStack+1)
	for i := 0; i < MaxStack; i++ {
		ok = append(ok, pushInt())
	}
	for i := 0; i < MaxStack; i++ {
		ok = append(ok, pop())
	}
	ok = append(ok, methodReturn())
	assertStatus(t, ok, 1, Config{}, Loadable)

	bad := append(ok[:0:0], ok...)
	bad = append([]Instr{pushInt()}, bad...) // 多压一个 -> 最深 33
	assertStatus(t, bad, 1, Config{}, Rejected)
}
