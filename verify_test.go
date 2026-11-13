package netsec

import (
	"strings"
	"testing"
)

func mustReject(t *testing.T, res Result) *Rejection {
	t.Helper()
	if res.Verdict != Rejected {
		t.Fatalf("期望 Rejected，得到 %v", res.Verdict)
	}
	t.Logf("%s", res.Rejection)
	return res.Rejection
}

func hasRetEdge(edges []RetEdge, chain []int, retPC, target int) bool {
	for _, e := range edges {
		if e.RetPC != retPC || e.Target != target || len(e.CallChain) != len(chain) {
			continue
		}
		ok := true
		for i := range chain {
			if e.CallChain[i] != chain[i] {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// 同一清理段被两个合法调用方共享：各自返回到自己的调用点，
// 两个调用点的返回位置不能并成“任意可跳目标”。
func TestSharedCleanupTwoCallers(t *testing.T) {
	code := []Instr{
		Jsr(4),         // 0: 调用方 A，返回位置 1
		Jsr(4),         // 1: 调用方 B，返回位置 2
		Return(),       // 2
		Goto(2),        // 3: 填充，不可达
		StoreReturn(0), // 4: 共享清理段
		Ret(0),         // 5
	}
	res := Verify(code, Options{})
	if res.Verdict != Safe {
		t.Fatalf("期望 Safe，得到 %v：%v", res.Verdict, res.Rejection)
	}
	if !hasRetEdge(res.RetEdges, []int{0}, 5, 1) {
		t.Errorf("缺少 ret 边 调用点链[0] -> 1，实际 %v", res.RetEdges)
	}
	if !hasRetEdge(res.RetEdges, []int{1}, 5, 2) {
		t.Errorf("缺少 ret 边 调用点链[1] -> 2，实际 %v", res.RetEdges)
	}
	if len(res.RetEdges) != 2 {
		t.Errorf("ret 边应精确为 2 条（按调用点区分），实际 %v", res.RetEdges)
	}
}

// 外层令牌被内层 ret 使用：必须拒绝，并输出拒绝前的调用点链。
func TestInnerRetUsesOuterToken(t *testing.T) {
	code := []Instr{
		Jsr(3),         // 0: 外层调用，令牌属于帧 0
		Return(),       // 1
		Goto(1),        // 2: 填充
		StoreReturn(0), // 3: 外层令牌存入槽 0
		Jsr(6),         // 4: 内层调用，令牌属于帧 1
		Return(),       // 5
		StoreReturn(1), // 6: 内层令牌存入槽 1
		Ret(0),         // 7: 恶意：用外层令牌从内层返回
	}
	rej := mustReject(t, Verify(code, Options{}))
	if rej.PC != 7 {
		t.Errorf("拒绝点应为 pc=7，实际 %d", rej.PC)
	}
	if len(rej.CallChain) != 2 || rej.CallChain[0] != 0 || rej.CallChain[1] != 4 {
		t.Errorf("调用点链应为 [0 4]，实际 %v", rej.CallChain)
	}
	if !strings.Contains(rej.Reason, "最内层") {
		t.Errorf("拒绝原因应指出令牌不属于最内层调用：%s", rej.Reason)
	}
}

// ret 之后旧别名失效：调用方缓存的令牌再加载必须拒绝。
func TestStaleAliasAfterRet(t *testing.T) {
	code := []Instr{
		Jsr(4),         // 0
		LoadReturn(0),  // 1: 子程序已返回，槽 0 中的旧别名已失效
		Pop(),          // 2
		Return(),       // 3
		StoreReturn(0), // 4
		Ret(0),         // 5
	}
	rej := mustReject(t, Verify(code, Options{}))
	if rej.PC != 1 {
		t.Errorf("拒绝点应为 pc=1，实际 %d", rej.PC)
	}
	if len(rej.CallChain) != 0 {
		t.Errorf("调用点链应为空，实际 %v", rej.CallChain)
	}
	if !strings.Contains(rej.Reason, "失效") {
		t.Errorf("拒绝原因应指出令牌失效：%s", rej.Reason)
	}
}

// 普通控制流环再次调用同一子程序：合法，不得误判递归。
func TestLoopReentry(t *testing.T) {
	code := []Instr{
		Jsr(4),         // 0: 循环内调用，返回位置 1
		Branch(0, 2),   // 1: 非确定分支：回环或退出
		Return(),       // 2
		Goto(2),        // 3: 填充
		StoreReturn(0), // 4
		Ret(0),         // 5
	}
	res := Verify(code, Options{})
	if res.Verdict != Safe {
		t.Fatalf("期望 Safe，得到 %v：%v", res.Verdict, res.Rejection)
	}
	for _, e := range res.RetEdges {
		if e.Target != 1 || len(e.CallChain) != 1 || e.CallChain[0] != 0 {
			t.Errorf("循环再入的 ret 边应恒为 调用点链[0] -> 1，实际 %v", e)
		}
	}
}

// 活动调用中再次进入同一子程序：递归，拒绝。
func TestRecursionRejected(t *testing.T) {
	code := []Instr{
		Jsr(3),         // 0
		Return(),       // 1
		Goto(1),        // 2: 填充
		StoreReturn(0), // 3
		Jsr(3),         // 4: 子程序在活动调用中再次进入自身
		Ret(0),         // 5
	}
	rej := mustReject(t, Verify(code, Options{}))
	if rej.PC != 4 {
		t.Errorf("拒绝点应为 pc=4，实际 %d", rej.PC)
	}
	if len(rej.CallChain) != 1 || rej.CallChain[0] != 0 {
		t.Errorf("调用点链应为 [0]，实际 %v", rej.CallChain)
	}
	if !strings.Contains(rej.Reason, "递归") {
		t.Errorf("拒绝原因应指出递归：%s", rej.Reason)
	}
}

// nestedProgram 构造 depth 层嵌套调用程序：第 i 层子程序在 base+3*i，
// 执行 storeReturn i、jsr 下一层、ret i；最后一层只有 storeReturn + ret。
func nestedProgram(depth int) []Instr {
	code := []Instr{Jsr(2), Return()}
	for i := 0; i < depth; i++ {
		code = append(code, StoreReturn(i))
		if i+1 < depth {
			code = append(code, Jsr(len(code)+2))
		}
		code = append(code, Ret(i))
	}
	return code
}

// 嵌套 4 层合法。
func TestNestingFourLevelsOK(t *testing.T) {
	res := Verify(nestedProgram(4), Options{})
	if res.Verdict != Safe {
		t.Fatalf("4 层嵌套应 Safe，得到 %v：%v", res.Verdict, res.Rejection)
	}
}

// 嵌套第 5 层超出上限，拒绝并给出完整调用点链。
func TestNestingFiveLevelsRejected(t *testing.T) {
	rej := mustReject(t, Verify(nestedProgram(5), Options{}))
	if !strings.Contains(rej.Reason, "嵌套") {
		t.Errorf("拒绝原因应指出嵌套超限：%s", rej.Reason)
	}
	if len(rej.CallChain) != 4 {
		t.Errorf("调用点链长度应为 4，实际 %v", rej.CallChain)
	}
}

// 预算耗尽：结论为 Unknown 而非 Safe。
func TestBudgetExhaustion(t *testing.T) {
	code := []Instr{
		Jsr(4),
		Branch(0, 2),
		Return(),
		Goto(2),
		StoreReturn(0),
		Ret(0),
	}
	res := Verify(code, Options{Budget: 2})
	if res.Verdict != Unknown {
		t.Fatalf("预算耗尽应得 Unknown，得到 %v", res.Verdict)
	}
}

// 方法 return 时操作数栈非空：拒绝。
func TestReturnWithNonEmptyOperandStack(t *testing.T) {
	code := []Instr{
		PushInt(1),
		Return(),
	}
	rej := mustReject(t, Verify(code, Options{}))
	if !strings.Contains(rej.Reason, "操作数栈非空") {
		t.Errorf("拒绝原因应指出操作数栈非空：%s", rej.Reason)
	}
}

// 方法 return 时活动调用栈非空：拒绝。
func TestReturnInsideSubroutine(t *testing.T) {
	code := []Instr{
		Jsr(2),
		Return(),
		Return(), // 2: 在子程序内直接 return
	}
	rej := mustReject(t, Verify(code, Options{}))
	if !strings.Contains(rej.Reason, "调用栈非空") {
		t.Errorf("拒绝原因应指出调用栈非空：%s", rej.Reason)
	}
}

// 整数与返回令牌不可转换：storeInt 存令牌必须拒绝。
func TestIntTokenNotConvertible(t *testing.T) {
	code := []Instr{
		Jsr(3),
		Return(),
		Goto(1),
		StoreInt(0), // 3: 栈顶是返回令牌
		Ret(0),
	}
	rej := mustReject(t, Verify(code, Options{}))
	if !strings.Contains(rej.Reason, "不可转换") {
		t.Errorf("拒绝原因应指出类型不可转换：%s", rej.Reason)
	}
}

// 合流时操作数栈不相容：拒绝。
func TestStackMergeIncompatible(t *testing.T) {
	code := []Instr{
		Branch(1, 2), // 0: 一条路径压栈，一条不压
		PushInt(5),   // 1
		Return(),     // 2: 合流点栈高不一致
	}
	rej := mustReject(t, Verify(code, Options{}))
	if !strings.Contains(rej.Reason, "栈不相容") {
		t.Errorf("拒绝原因应指出栈不相容：%s", rej.Reason)
	}
}

// 合流时局部槽不相容变不可用：一条路径写整数、一条不写，
// 合流后加载该槽必须拒绝。
func TestSlotMergeUnavailable(t *testing.T) {
	code := []Instr{
		Branch(1, 3), // 0
		PushInt(7),   // 1
		StoreInt(0),  // 2
		LoadInt(0),   // 3: 合流后槽 0 不可用
		Pop(),        // 4
		Return(),     // 5
	}
	rej := mustReject(t, Verify(code, Options{}))
	if rej.PC != 3 {
		t.Errorf("拒绝点应为 pc=3，实际 %d", rej.PC)
	}
	if !strings.Contains(rej.Reason, "不可用") {
		t.Errorf("拒绝原因应指出槽不可用：%s", rej.Reason)
	}
}
