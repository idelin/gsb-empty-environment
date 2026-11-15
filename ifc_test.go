package netsec

import (
	"errors"
	"sync"
	"testing"
)

func newEval(reg *Registry, audit *AuditLog, req string) *Evaluator {
	return &Evaluator{Reg: reg, Audit: audit, RequestID: req}
}

// 嵌套条件泄露:外层条件读取秘密来源 0,分支内部即使把来源 1
// 合法降密,外围控制依赖(pc)仍留在值上,公开输出必须被拒绝。
func TestNestedConditionLeak(t *testing.T) {
	reg := NewRegistry()
	audit := NewAuditLog()
	ev := newEval(reg, audit, "req-1")
	ev.SourceValues[0] = 1 // 让外层条件进入 Then 分支

	auth := reg.Issue("req-1", "inner", LabelOf(1))
	prog := []Expr{
		If{
			Cond: Source{ID: 0},
			Then: Output{Val: Declassify{NodeID: "inner", Auth: auth, Val: Source{ID: 1}}},
			Else: Const{V: 0},
		},
	}
	out, err := ev.Run(prog)
	if !errors.Is(err, ErrNonPublicOutput) {
		t.Fatalf("expected ErrNonPublicOutput, got out=%v err=%v", out, err)
	}
	if out != nil {
		t.Fatalf("no output may escape on failure, got %v", out)
	}
	// 失败的求值不得消耗授权。
	if reg.Live() != 1 {
		t.Fatalf("authorization must be restored after rollback, live=%d", reg.Live())
	}
	if audit.Count(EvOutput) != 0 {
		t.Fatalf("no public output event may be audited")
	}
	if audit.Count(EvRollback) != 1 {
		t.Fatalf("expected one rollback event")
	}
}

// 授权重用:同一授权在同一请求内用两次,第二次必须失败;
// 跨请求引用必须失败;伪造授权(非注册器签发)必须失败。
func TestAuthorizationReuse(t *testing.T) {
	reg := NewRegistry()
	audit := NewAuditLog()

	auth := reg.Issue("req-1", "n1", LabelOf(0))
	prog := []Expr{
		Output{Val: Declassify{NodeID: "n1", Auth: auth, Val: Source{ID: 0}}},
		Output{Val: Declassify{NodeID: "n1", Auth: auth, Val: Source{ID: 0}}},
	}
	if _, err := newEval(reg, audit, "req-1").Run(prog); !errors.Is(err, ErrAuthExhausted) {
		t.Fatalf("second use of same authorization must fail with ErrAuthExhausted, got %v", err)
	}
	// 第一次消费被回滚,授权仍可使用。
	if _, err := newEval(reg, audit, "req-1").Run([]Expr{
		Output{Val: Declassify{NodeID: "n1", Auth: auth, Val: Source{ID: 0}}},
	}); err != nil {
		t.Fatalf("rolled-back authorization must be usable again, got %v", err)
	}
	// 现在授权真正被消耗,跨请求引用必须失败。
	if _, err := newEval(reg, audit, "req-2").Run([]Expr{
		Output{Val: Declassify{NodeID: "n1", Auth: auth, Val: Source{ID: 0}}},
	}); !errors.Is(err, ErrAuthExhausted) {
		t.Fatalf("cross-request reuse of consumed authorization must fail, got %v", err)
	}

	// 绑定请求不匹配:为 req-3 签发的授权不能用于 req-4。
	auth2 := reg.Issue("req-3", "n1", LabelOf(0))
	if _, err := newEval(reg, audit, "req-4").Run([]Expr{
		Output{Val: Declassify{NodeID: "n1", Auth: auth2, Val: Source{ID: 0}}},
	}); !errors.Is(err, ErrAuthRequest) {
		t.Fatalf("authorization bound to another request must fail, got %v", err)
	}

	// 绑定节点不匹配。
	auth3 := reg.Issue("req-5", "node-a", LabelOf(0))
	if _, err := newEval(reg, audit, "req-5").Run([]Expr{
		Output{Val: Declassify{NodeID: "node-b", Auth: auth3, Val: Source{ID: 0}}},
	}); !errors.Is(err, ErrAuthNode) {
		t.Fatalf("authorization bound to another node must fail, got %v", err)
	}

	// 伪造授权:攻击者自己拼的对象,注册器必须拒绝。
	forged := &Authorization{RequestID: "req-6", NodeID: "n1", Allowed: LabelOf(0)}
	if _, err := newEval(reg, audit, "req-6").Run([]Expr{
		Output{Val: Declassify{NodeID: "n1", Auth: forged, Val: Source{ID: 0}}},
	}); !errors.Is(err, ErrAuthForeign) {
		t.Fatalf("forged authorization must fail, got %v", err)
	}
}

// 失败回滚:程序先产生公开输出、再合法降密,最后输出秘密值失败。
// 结果是:不返回任何部分输出,授权被归还,审计中只有回滚没有输出。
func TestFailureRollback(t *testing.T) {
	reg := NewRegistry()
	audit := NewAuditLog()
	ev := newEval(reg, audit, "req-1")

	auth := reg.Issue("req-1", "d1", LabelOf(0))
	prog := []Expr{
		Output{Val: Const{V: 42}}, // 已成功的公开输出
		Output{Val: Declassify{NodeID: "d1", Auth: auth, Val: Source{ID: 0}}}, // 已消费的授权
		Output{Val: Source{ID: 1}}, // 触发失败
	}
	out, err := ev.Run(prog)
	if !errors.Is(err, ErrNonPublicOutput) {
		t.Fatalf("expected ErrNonPublicOutput, got %v", err)
	}
	if out != nil {
		t.Fatalf("partial public output must not escape, got %v", out)
	}
	if reg.Live() != 1 {
		t.Fatalf("consumed authorization must be restored, live=%d", reg.Live())
	}
	// 授权确实可再次使用,证明归还生效。
	if _, err := newEval(reg, audit, "req-1").Run([]Expr{
		Output{Val: Declassify{NodeID: "d1", Auth: auth, Val: Source{ID: 0}}},
	}); err != nil {
		t.Fatalf("restored authorization must be usable, got %v", err)
	}
}

// 正常定点降密:授权允许的来源被移除后公开输出成功;
// 授权未允许的来源仍阻止输出;局部变量与加法正确传播标签。
func TestTargetedDeclassify(t *testing.T) {
	reg := NewRegistry()
	audit := NewAuditLog()
	ev := newEval(reg, audit, "req-1")

	auth := reg.Issue("req-1", "d1", LabelOf(0)|LabelOf(1))
	prog := []Expr{
		Let{Name: "x", Val: Add{L: Source{ID: 0}, R: Source{ID: 1}}, Body: Output{Val: Declassify{NodeID: "d1", Auth: auth, Val: Add{L: Var{Name: "x"}, R: Const{V: 2}}}}},
		Output{Val: If{Cond: Const{V: 1}, Then: Const{V: 7}, Else: Source{ID: 3}}},
	}
	// Source 值未知,这里只验证标签语义:输出成功即标签为空。
	if _, err := ev.Run(prog); err != nil {
		t.Fatalf("targeted declassification must succeed, got %v", err)
	}
	if got := audit.Count(EvOutput); got != 2 {
		t.Fatalf("expected 2 output events, got %d", got)
	}
	if got := audit.Count(EvDeclassify); got != 1 {
		t.Fatalf("expected 1 declassify event, got %d", got)
	}

	// 授权只允许来源 0,值上还带来源 1,输出必须失败。
	auth2 := reg.Issue("req-1", "d2", LabelOf(0))
	if _, err := newEval(reg, audit, "req-1").Run([]Expr{
		Output{Val: Declassify{NodeID: "d2", Auth: auth2, Val: Add{L: Source{ID: 0}, R: Source{ID: 1}}}},
	}); !errors.Is(err, ErrNonPublicOutput) {
		t.Fatalf("declassify must only remove allowed sources, got %v", err)
	}
}

// 并发原子性:多个并发请求竞争同一授权,注册器保证恰好一个消费成功;
// 每个请求的求值要么完整成功要么整体回滚,授权总数守恒。
func TestConcurrentAtomicity(t *testing.T) {
	reg := NewRegistry()
	audit := NewAuditLog()

	const racers = 32
	auth := reg.Issue("req-shared", "d1", LabelOf(0))
	prog := []Expr{
		Output{Val: Declassify{NodeID: "d1", Auth: auth, Val: Source{ID: 0}}},
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded := 0
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 所有竞争者使用授权绑定的同一请求 ID,纯粹比拼原子消费。
			if _, err := newEval(reg, audit, "req-shared").Run(prog); err == nil {
				mu.Lock()
				succeeded++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if succeeded != 1 {
		t.Fatalf("exactly one racer may consume the authorization, got %d", succeeded)
	}
	if got := audit.Count(EvOutput); got != 1 {
		t.Fatalf("exactly one public output may be audited, got %d", got)
	}
	// 失败者的回滚不得复活已消费的授权。
	if reg.Live() != 0 {
		t.Fatalf("authorization must stay consumed, live=%d", reg.Live())
	}

	// 授权守恒:并发消费 N 个独立授权,全部成功且注册器清空。
	const n = 64
	auths := make([]*Authorization, n)
	for i := range auths {
		auths[i] = reg.Issue("req-shared", "d2", LabelOf(0))
	}
	var wg2 sync.WaitGroup
	var mu2 sync.Mutex
	ok := 0
	for i := 0; i < n; i++ {
		wg2.Add(1)
		go func(a *Authorization) {
			defer wg2.Done()
			if err := reg.Consume(a, "req-shared", "d2"); err == nil {
				mu2.Lock()
				ok++
				mu2.Unlock()
			}
		}(auths[i])
	}
	wg2.Wait()
	if ok != n || reg.Live() != 0 {
		t.Fatalf("all %d distinct authorizations must be consumed exactly once, ok=%d live=%d", n, ok, reg.Live())
	}
}
