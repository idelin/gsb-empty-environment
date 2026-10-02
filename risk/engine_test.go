package risk

import (
	"testing"
	"time"
)

var base = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }

// 旧规则 v1：10 分钟内转账超 3 次且累计金额超 10000 才拦截。
func rulesV1() *RuleSet {
	return &RuleSet{
		Version: "v1",
		Rules: []Rule{{
			Name:      "freq+amount",
			Window:    10 * time.Minute,
			MaxCount:  3,
			MaxAmount: 10000,
		}},
	}
}

// 新规则 v2（更严格）：10 分钟内超 1 次且累计超 5000 即拦截。
func rulesV2() *RuleSet {
	return &RuleSet{
		Version: "v2",
		Rules: []Rule{{
			Name:      "freq+amount",
			Window:    10 * time.Minute,
			MaxCount:  1,
			MaxAmount: 5000,
		}},
	}
}

// 验收场景：t=100 规则热切换为更严格阈值；
// t=90 开始的长事务按旧规则裁定；t=101 发起的新请求按新规则拦截；
// 审计日志中两者标记不同规则版本号。
func TestHotSwitchAcceptance(t *testing.T) {
	e := New(rulesV1(), nil)

	// t=90/91/92：长事务 S-long 前三笔，按 v1 放行。
	for i, sec := range []int{90, 91, 92} {
		d := e.Evaluate(Txn{ID: "L" + string(rune('1'+i)), AccountID: "acc-A", SessionID: "S-long", Amount: 2000, At: at(sec)})
		if !d.Allowed || d.RuleVersion != "v1" {
			t.Fatalf("t=%d 长事务应被 v1 放行, got %+v", sec, d)
		}
	}

	// t=100：规则库热切换为更严格的 v2。
	e.UpdateRules(rulesV2())
	if got := e.CurrentVersion(); got != "v2" {
		t.Fatalf("当前版本应为 v2, got %s", got)
	}

	// t=101：长事务第 4 笔。v2 下 count=4>1 且 sum=8000>5000 本应拦截，
	// 但会话钉住 v1：count=4>3 但 sum=8000 未超 10000，故按旧规则放行。
	d := e.Evaluate(Txn{ID: "L4", AccountID: "acc-A", SessionID: "S-long", Amount: 2000, At: at(101)})
	if !d.Allowed {
		t.Fatalf("长事务应按旧规则 v1 放行, got %+v", d)
	}
	if d.RuleVersion != "v1" {
		t.Fatalf("长事务应标记 v1, got %s", d.RuleVersion)
	}

	// t=101：全新会话 S-new 第一笔放行（count=1 未超 v2 阈值）。
	d = e.Evaluate(Txn{ID: "N1", AccountID: "acc-B", SessionID: "S-new", Amount: 3000, At: at(101)})
	if !d.Allowed || d.RuleVersion != "v2" {
		t.Fatalf("新会话首笔应按 v2 放行, got %+v", d)
	}

	// t=102：新会话第二笔，v2 下 count=2>1 且 sum=6000>5000，严格拦截。
	// （同样的交易在 v1 下 count=2<=3 本可放行。）
	d = e.Evaluate(Txn{ID: "N2", AccountID: "acc-B", SessionID: "S-new", Amount: 3000, At: at(102)})
	if d.Allowed {
		t.Fatalf("新会话第二笔应按 v2 拦截, got %+v", d)
	}
	if d.RuleVersion != "v2" || d.MatchedRule != "freq+amount" {
		t.Fatalf("拦截应标记 v2 且命中规则, got %+v", d)
	}

	// 审计日志：长事务全部 v1，新会话全部 v2。
	for _, entry := range e.AuditLog() {
		want := map[string]string{"S-long": "v1", "S-new": "v2"}[entry.SessionID]
		if entry.RuleVersion != want {
			t.Fatalf("审计日志会话 %s 应标记 %s, got %s", entry.SessionID, want, entry.RuleVersion)
		}
	}
}

// 会话结束后平稳过渡：同一会话 ID 的后续交易改用新版本。
func TestSessionEndTransitionsToNewVersion(t *testing.T) {
	e := New(rulesV1(), nil)
	e.Evaluate(Txn{ID: "A1", AccountID: "acc-C", SessionID: "S-x", Amount: 100, At: at(10)})
	e.UpdateRules(rulesV2())
	e.EndSession("S-x")
	d := e.Evaluate(Txn{ID: "A2", AccountID: "acc-C", SessionID: "S-x", Amount: 100, At: at(20)})
	if d.RuleVersion != "v2" {
		t.Fatalf("会话结束后应切换到 v2, got %s", d.RuleVersion)
	}
}

// 滑动窗口时效性：窗口外的历史交易不计入频次。
func TestSlidingWindowExpiry(t *testing.T) {
	e := New(rulesV2(), nil) // 10 分钟窗口，超 1 次且超 5000 拦截
	e.Evaluate(Txn{ID: "W1", AccountID: "acc-D", SessionID: "S-w", Amount: 4000, At: at(0)})
	// t=601 已滑出 10 分钟窗口，历史不再计入，count=1 未超阈值，放行。
	d := e.Evaluate(Txn{ID: "W2", AccountID: "acc-D", SessionID: "S-w", Amount: 4000, At: at(601)})
	if !d.Allowed {
		t.Fatalf("窗口外交易不应计入频次, got %+v", d)
	}
	// t=602 紧接一笔：窗口内 count=2>1 且 sum=8000>5000，拦截。
	d = e.Evaluate(Txn{ID: "W3", AccountID: "acc-D", SessionID: "S-w", Amount: 4000, At: at(602)})
	if d.Allowed {
		t.Fatalf("窗口内超频应拦截, got %+v", d)
	}
}

// 单笔金额上限与频次条件独立生效。
func TestSingleAmountLimit(t *testing.T) {
	rs := &RuleSet{Version: "v3", Rules: []Rule{{
		Name: "single-cap", Window: 10 * time.Minute, MaxSingleAmount: 50000,
	}}}
	e := New(rs, nil)
	d := e.Evaluate(Txn{ID: "S1", AccountID: "acc-E", SessionID: "S-s", Amount: 60000, At: at(0)})
	if d.Allowed || d.MatchedRule != "single-cap" {
		t.Fatalf("超单笔上限应拦截, got %+v", d)
	}
}
