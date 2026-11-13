package netsec

import (
	"strings"
	"testing"
)

func TestFindShortestExploitPendingReview(t *testing.T) {
	exploit := FindShortestExploit(ModePendingReview, DefaultAlphabet(), 3)
	if exploit == nil {
		t.Fatal("待审版本应能搜到违规动作序列")
	}
	// 最短利用长度为 1：进入 Closing 后直接 Call 当前句柄即成功。
	if len(exploit.Script) != 1 || exploit.Script[0].Kind != ActCallCurrent {
		t.Fatalf("最短利用应为单动作 Call，实际 %v", exploit.Script)
	}
	if exploit.Violation.Kind != ViolationCallAfterClosing {
		t.Fatalf("违规类型应为 CallAfterClosing，实际 %s", exploit.Violation.Kind)
	}
	// 完整重入栈：违规发生在 Close(A) 的回调上下文内。
	stack := strings.Join(exploit.Violation.Stack, " -> ")
	if !strings.Contains(stack, "Close(A)") || !strings.Contains(stack, "Call(h1)") {
		t.Fatalf("重入栈应包含 Close(A) 与 Call(h1)，实际 %s", stack)
	}
	if !strings.Contains(exploit.Trace, "BeforeRelease(A, h1)") {
		t.Fatalf("轨迹应包含回调帧，实际:\n%s", exploit.Trace)
	}
	t.Logf("最短利用: %v\n重入栈: %s\n轨迹:\n%s",
		exploit.Script, stack, exploit.Trace)
}

func TestFindShortestDoubleReleaseExploit(t *testing.T) {
	// 去掉 Call 动作，专门搜索同一句柄二次 Release。
	exploit := FindShortestExploit(ModePendingReview, MoveCloseAlphabet(), 3)
	if exploit == nil {
		t.Fatal("待审版本应能搜到二次释放序列")
	}
	// 最短二次释放长度为 2：MoveTo(h1 -> B) 后 Close(B)。
	if len(exploit.Script) != 2 {
		t.Fatalf("最短二次释放应为 2 个动作，实际 %v", exploit.Script)
	}
	if exploit.Script[0].Kind != ActMoveCurrent || exploit.Script[1] != (Action{Kind: ActClose, Target: "B"}) {
		t.Fatalf("利用序列应为 [MoveTo->B, Close(B)]，实际 %v", exploit.Script)
	}
	if exploit.Violation.Kind != ViolationDoubleRelease {
		t.Fatalf("违规类型应为 DoubleRelease，实际 %s", exploit.Violation.Kind)
	}
	stack := strings.Join(exploit.Violation.Stack, " -> ")
	if !strings.Contains(stack, "Close(A)") || !strings.Contains(stack, "Release(h1)") {
		t.Fatalf("重入栈应呈现嵌套释放，实际 %s", stack)
	}
	t.Logf("最短二次释放: %v\n重入栈: %s\n轨迹:\n%s",
		exploit.Script, stack, exploit.Trace)
}

func TestNoExploitOnReviewed(t *testing.T) {
	if exploit := FindShortestExploit(ModeReviewed, DefaultAlphabet(), 3); exploit != nil {
		t.Fatalf("修订版在深度 3 内不应存在利用，实际 %v\n%s", exploit.Script, exploit.Trace)
	}
}
