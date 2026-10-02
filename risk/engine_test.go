package risk

import (
	"fmt"
	"sync"
	"testing"
)

// v1: 10 分钟内转账超 3 次且金额超 1 万 -> 拦截
func rulesV1() *RuleSet {
	return &RuleSet{
		Version: "v1",
		Rules: []Rule{
			{Name: "freq+amount", Window: 600, MaxCount: 3, MaxAmount: 10000},
		},
	}
}

// v2 (更严格): 10 分钟内转账超 1 次且金额超 5 千 -> 拦截
func rulesV2() *RuleSet {
	return &RuleSet{
		Version: "v2",
		Rules: []Rule{
			{Name: "freq+amount", Window: 600, MaxCount: 1, MaxAmount: 5000},
		},
	}
}

// 验收边界：t=100 规则热切换为更严格阈值；
// t=90 开始的长事务按旧规则(v1)裁定，t=101 的新请求按新规则(v2)拦截；
// 审计日志中两者标记不同规则版本号。
func TestHotSwapAcceptanceBoundary(t *testing.T) {
	clock := NewManualClock(0)
	audit := NewAuditLog()
	engine := NewEngine(clock, rulesV1(), audit)

	// t=90: 长事务 S1 开始，钉住 v1
	clock.Set(90)
	if err := engine.BeginSession("S1", "alice"); err != nil {
		t.Fatalf("BeginSession: %v", err)
	}

	// t=91, t=92: S1 内前两笔转账（小额，放行）
	clock.Set(91)
	if r := engine.Evaluate("S1", Event{TxID: "tx1", Account: "alice", Amount: 100, Timestamp: 91}); r.Decision != DecisionAllow {
		t.Fatalf("tx1: want ALLOW, got %s", r.Decision)
	}
	clock.Set(92)
	if r := engine.Evaluate("S1", Event{TxID: "tx2", Account: "alice", Amount: 100, Timestamp: 92}); r.Decision != DecisionAllow {
		t.Fatalf("tx2: want ALLOW, got %s", r.Decision)
	}

	// t=100: 规则库热切换为更严格的 v2
	clock.Set(100)
	engine.UpdateRules(rulesV2())

	// t=101: S1（t=90 开始的长事务）提交第 3 笔，金额 2 万。
	// 旧规则 v1：窗口内 3 次，未超 3 次 -> 放行。
	// 若错误地用了 v2：3 > 1 且 20000 > 5000 -> 会被拦截。
	clock.Set(101)
	r := engine.Evaluate("S1", Event{TxID: "tx3", Account: "alice", Amount: 20000, Timestamp: 101})
	if r.Decision != DecisionAllow {
		t.Fatalf("tx3 (old session): want ALLOW under v1, got %s (%s)", r.Decision, r.Reason)
	}
	if r.RuleVersion != "v1" {
		t.Fatalf("tx3: want rule version v1, got %s", r.RuleVersion)
	}

	// t=101: 同一账户发起全新请求（无会话），必须按新规则 v2 严格拦截。
	r = engine.Evaluate("", Event{TxID: "tx4", Account: "alice", Amount: 20000, Timestamp: 101})
	if r.Decision != DecisionDeny {
		t.Fatalf("tx4 (new request): want DENY under v2, got %s", r.Decision)
	}
	if r.RuleVersion != "v2" {
		t.Fatalf("tx4: want rule version v2, got %s", r.RuleVersion)
	}

	// 审计日志：tx3 标记 v1，tx4 标记 v2，两个版本共存。
	entries := audit.Entries()
	byTx := make(map[string]Entry)
	for _, e := range entries {
		byTx[e.TxID] = e
	}
	if byTx["tx3"].RuleVersion != "v1" || byTx["tx3"].Decision != DecisionAllow {
		t.Fatalf("audit tx3: got %+v", byTx["tx3"])
	}
	if byTx["tx4"].RuleVersion != "v2" || byTx["tx4"].Decision != DecisionDeny {
		t.Fatalf("audit tx4: got %+v", byTx["tx4"])
	}
}

// 会话结束后，同一账户的新请求立即使用当前（新）规则版本。
func TestSessionEndFallsBackToCurrentRules(t *testing.T) {
	clock := NewManualClock(0)
	audit := NewAuditLog()
	engine := NewEngine(clock, rulesV1(), audit)

	if err := engine.BeginSession("S1", "bob"); err != nil {
		t.Fatal(err)
	}
	engine.UpdateRules(rulesV2())
	engine.EndSession("S1")

	r := engine.Evaluate("S1", Event{TxID: "tx9", Account: "bob", Amount: 100, Timestamp: 200})
	if r.RuleVersion != "v2" {
		t.Fatalf("after EndSession: want v2, got %s", r.RuleVersion)
	}
}

// 规则热切换与并发评估同时发生时，决策必须始终使用某一个完整版本，
// 且审计版本号只能是 v1 或 v2。
func TestConcurrentHotSwap(t *testing.T) {
	clock := NewManualClock(0)
	audit := NewAuditLog()
	engine := NewEngine(clock, rulesV1(), audit)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				engine.Evaluate("", Event{
					TxID:      fmt.Sprintf("tx-%d-%d", i, j),
					Account:   fmt.Sprintf("acct-%d", i),
					Amount:    1,
					Timestamp: int64(j),
				})
			}
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for k := 0; k < 50; k++ {
			engine.UpdateRules(rulesV2())
			engine.UpdateRules(rulesV1())
		}
	}()
	wg.Wait()

	for _, e := range audit.Entries() {
		if e.TxID == "-" {
			continue // swap marker entries
		}
		if e.RuleVersion != "v1" && e.RuleVersion != "v2" {
			t.Fatalf("unexpected rule version in audit: %+v", e)
		}
	}
}

// 滑动窗口语义：窗口外的历史事件不计入频次。
func TestSlidingWindowExpiry(t *testing.T) {
	clock := NewManualClock(0)
	audit := NewAuditLog()
	engine := NewEngine(clock, rulesV1(), audit)

	// t=0..3 共 4 笔大额，第 4 笔触发 v1（4 > 3 且金额 > 1 万）
	for i := int64(0); i <= 3; i++ {
		engine.Evaluate("", Event{TxID: fmt.Sprintf("a%d", i), Account: "carol", Amount: 20000, Timestamp: i})
	}
	// t=1000：窗口(600s)内只有当前这一笔，不再触发
	r := engine.Evaluate("", Event{TxID: "a4", Account: "carol", Amount: 20000, Timestamp: 1000})
	if r.Decision != DecisionAllow {
		t.Fatalf("window expiry: want ALLOW, got %s (%s)", r.Decision, r.Reason)
	}
}
