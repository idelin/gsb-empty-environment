package netsec

import (
	"strings"
	"testing"
)

// 完整模型（至多两次轮换、消息可丢失可重复、含重试）必须发现违例，
// 且轨迹可回放、最后一步违例、密钥代不超过 2。
func TestFullConfigFindsShortestViolation(t *testing.T) {
	cfg := DefaultConfig()
	res, err := Explore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Violation == nil {
		t.Fatalf("expected a violation, explored %d states", res.Reachable)
	}
	v := res.Violation
	t.Logf("reachable states before violation: %d, depth: %d", res.Reachable, v.Depth())
	for i, step := range v.Trace {
		t.Logf("  %2d. %s", i+1, step.Name)
	}
	// 轨迹必须能逐步回放，且只有最后一步违例。
	for i, step := range v.Trace {
		if i < len(v.Trace)-1 && step.Violates {
			t.Fatalf("step %d violates before the end of the trace", i)
		}
	}
	if !v.Trace[len(v.Trace)-1].Violates {
		t.Fatal("last step of the trace must be the violation")
	}
	if _, err := Replay(cfg, v.Trace); err != nil {
		t.Fatalf("trace does not replay: %v", err)
	}
	// 至多两次轮换：任何消息与端点的密钥代都不超过 2。
	final := v.Trace[len(v.Trace)-1].Next
	for i, ep := range final.ep {
		if ep.cur > 2 || ep.derived > 2 {
			t.Fatalf("endpoint %d exceeded two rotations: %+v", i, ep)
		}
	}
}

// 最短违例轨迹应当呈现题目场景：双方完成安装后，
// 确认未达成共同认可，明文仍被交付。
func TestViolationTraceShape(t *testing.T) {
	res, err := Explore(DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if res.Violation == nil {
		t.Fatal("expected a violation")
	}
	last := res.Violation.Trace[len(res.Violation.Trace)-1]
	if !strings.Contains(last.Name, "VIOLATION") {
		t.Fatalf("unexpected final step: %s", last.Name)
	}
	// 违例发生在 gen>=1：初始代 0 始终被共同认可。
	if !strings.Contains(last.Name, "gen1") && !strings.Contains(last.Name, "gen2") {
		t.Fatalf("violation should involve a rotated key generation: %s", last.Name)
	}
}

// 与显式小状态参考实现交叉核对：可达状态数与违例深度必须一致。
func TestReferenceCrossCheck(t *testing.T) {
	cfg := RefConfig()
	states, violDepth, err := CountReachable(cfg)
	if err != nil {
		t.Fatal(err)
	}
	refStates, refViolDepth := RefCheck()
	t.Logf("main: %d states, violation at depth %d; reference: %d states, depth %d",
		states, violDepth, refStates, refViolDepth)
	if states != refStates {
		t.Fatalf("reachable state count mismatch: main=%d reference=%d", states, refStates)
	}
	if violDepth != refViolDepth {
		t.Fatalf("violation depth mismatch: main=%d reference=%d", violDepth, refViolDepth)
	}
	// 手工推导：发方需 派生/安装/确认/发数据 4 步，确认占满容量为 1 的信道，
	// 须先丢失或接收 1 步，收方需 派生/安装/接收 3 步，合计最短 8 步。
	if violDepth != 8 {
		t.Fatalf("hand-derived shortest violation depth is 8, got %d", violDepth)
	}
	// Explore 在同一配置下应给出相同深度的最短轨迹。
	res, err := Explore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Violation == nil || res.Violation.Depth() != violDepth {
		t.Fatalf("Explore depth %v, want %d", res.Violation, violDepth)
	}
}

// 修复变体：仅在密钥代被双方共同认可后才允许发数据，不变式必须成立。
func TestFixedProtocolHasNoViolation(t *testing.T) {
	cfg := DefaultConfig()
	cfg.RequireAckToSend = true
	res, err := Explore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res.Violation != nil {
		names := make([]string, len(res.Violation.Trace))
		for i, s := range res.Violation.Trace {
			names[i] = s.Name
		}
		t.Fatalf("fixed protocol still violates:\n%s", strings.Join(names, "\n"))
	}
	t.Logf("fixed protocol: %d reachable states, no violation", res.Reachable)
}

// 题目核心场景：双方完成新密钥安装但确认丢失后，
// 旧钥已销毁的端点重试只能进入不可恢复状态，无法回退到旧钥。
func TestRetryCannotRecoverDestroyedKey(t *testing.T) {
	cfg := DefaultConfig()
	s := Initial(cfg)
	// A 派生并安装 gen1，随后销毁 gen0；确认尚未得到对端认可。
	s.ep[0].derived = 1
	s.ep[0].cur = 1
	s.ep[0].announced |= 0b010
	s.ep[0].oldGone = true

	var retry *Step
	for _, step := range Successors(cfg, s) {
		if strings.HasPrefix(step.Name, "A.retry") {
			st := step
			retry = &st
		}
	}
	if retry == nil {
		t.Fatal("expected a retry transition for A")
	}
	if !retry.Next.ep[0].stuck {
		t.Fatalf("retry with destroyed old key must get stuck: %s", retry.Name)
	}
	// 不可恢复的端点不再有任何本地动作。
	if got := localSteps(cfg, retry.Next, 0); len(got) != 0 {
		t.Fatalf("stuck endpoint must have no local actions, got %d", len(got))
	}
	// 旧钥已销毁：gen0 的数据无法解密，只能丢弃。
	s2 := Initial(cfg)
	s2.ep[0] = retry.Next.ep[0]
	s2.ch[0][0] = Msg{kind: msgData, gen: 0}
	s2.n[0] = 1
	for _, step := range Successors(cfg, s2) {
		if strings.HasPrefix(step.Name, "A.recv-data") && step.Violates {
			t.Fatalf("destroyed key must not deliver plaintext: %s", step.Name)
		}
	}
}
