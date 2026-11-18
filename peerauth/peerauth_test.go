package peerauth

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeEvaluator 模拟 SecCode 代码要求判定：按完整审计令牌配置当前身份，
// 并统计每个令牌被实时判定的次数，用于证明缓存与重新判定的边界。
type fakeEvaluator struct {
	mu       sync.Mutex
	ident    map[AuditToken]Identity
	err      map[AuditToken]error
	evalCnt  map[AuditToken]int
	totalCnt int32
}

func newFakeEvaluator() *fakeEvaluator {
	return &fakeEvaluator{
		ident:   make(map[AuditToken]Identity),
		err:     make(map[AuditToken]error),
		evalCnt: make(map[AuditToken]int),
	}
}

func (f *fakeEvaluator) set(token AuditToken, identity Identity) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ident[token] = identity
}

func (f *fakeEvaluator) fail(token AuditToken, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err[token] = err
}

func (f *fakeEvaluator) clearError(token AuditToken) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.err, token)
}

func (f *fakeEvaluator) eval(token AuditToken) (Identity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	atomic.AddInt32(&f.totalCnt, 1)
	f.evalCnt[token]++
	if err, ok := f.err[token]; ok {
		return Identity{}, err
	}
	identity, ok := f.ident[token]
	if !ok {
		return Identity{}, errors.New("no code-signature identity for token")
	}
	return identity, nil
}

func (f *fakeEvaluator) count(token AuditToken) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.evalCnt[token]
}

func mustValidator(t *testing.T, evaluator IdentityEvaluator, grants ...Grant) *Validator {
	t.Helper()
	validator, err := NewValidator(evaluator, grants...)
	if err != nil {
		t.Fatalf("NewValidator() error = %v", err)
	}
	return validator
}

func check(t *testing.T, wantOK bool, err error, wantErr error, context string) {
	t.Helper()
	if wantOK {
		if err != nil {
			t.Fatalf("%s: unexpected error %v", context, err)
		}
		return
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("%s: error = %v, want %v", context, err, wantErr)
	}
}

var testGrants = []Grant{
	{
		TeamID:         "TEAMGOOD",
		SigningID:      "com.example.agent",
		PlatformBinary: false,
		AuthKeys:       []string{"agent.read", "agent.write"},
	},
	{
		TeamID:         "TEAMOTHER",
		SigningID:      "com.example.other",
		PlatformBinary: false,
		AuthKeys:       []string{"other.ping"},
	},
	{
		TeamID:         "",
		SigningID:      "com.apple.platform.syspolicy",
		PlatformBinary: true,
		AuthKeys:       []string{"platform.policy"},
	},
}

// TestPostCheckExec 覆盖“检查通过后 exec 另一个二进制”：
// exec 事件清除旧令牌缓存，且新审计令牌（新 pidversion）必须重新判定，
// 恶意身份既无法通过连接校验，也无法借用旧授权调用特权方法。
func TestPostCheckExec(t *testing.T) {
	evaluator := newFakeEvaluator()
	validator := mustValidator(t, evaluator.eval, testGrants...)

	benign := AuditToken{Pid: 100, PidVersion: 1, Euid: 501}
	afterExec := AuditToken{Pid: 100, PidVersion: 2, Euid: 501}
	evaluator.set(benign, Identity{
		TeamID:         "TEAMGOOD",
		SigningID:      "com.example.agent",
		PlatformBinary: false,
	})
	evaluator.set(afterExec, Identity{
		TeamID:         "EVILTEAM",
		SigningID:      "com.evil.implant",
		PlatformBinary: false,
	})

	check(t, true, validator.ValidateConnection(benign), nil, "benign connection")
	check(t, true, validator.Authorize(benign, "agent.write"), nil, "benign privileged call")
	if got := evaluator.count(benign); got != 1 {
		t.Fatalf("benign evaluations = %d, want 1 (connection result reused by authorization)", got)
	}

	validator.NotifyExec(benign.Pid)
	// 真实语义下，exec 后旧审计令牌的判定不再返回原身份：这里让其指向
	// 恶意二进制，验证旧结论已作废而不是沿用缓存。
	evaluator.set(benign, evaluator.ident[afterExec])

	check(t, false, validator.ValidateConnection(afterExec), ErrPeerNotRegistered, "re-validation after exec")
	check(t, false, validator.Authorize(afterExec, "agent.write"), ErrPeerNotRegistered, "post-exec privileged call")
	if got := evaluator.count(afterExec); got != 1 {
		t.Fatalf("post-exec evaluations = %d, want 1 (new pidversion forces fresh evaluation)", got)
	}

	// 旧审计令牌在 exec 后也已作废，必须重新判定而不是沿用缓存结论。
	check(t, false, validator.Authorize(benign, "agent.write"), ErrPeerNotRegistered, "stale token after exec")
	if got := evaluator.count(benign); got != 2 {
		t.Fatalf("benign re-evaluations after exec = %d, want 2", got)
	}
}

// TestPIDReuse 覆盖“pid 被复用”：同一 pid、不同 pidversion 的新进程
// 无法借用旧进程已通过的检查；euid 变化同样视为新审计令牌并重新判定。
func TestPIDReuse(t *testing.T) {
	evaluator := newFakeEvaluator()
	validator := mustValidator(t, evaluator.eval, testGrants...)

	oldProc := AuditToken{Pid: 200, PidVersion: 7, Euid: 501}
	reused := AuditToken{Pid: 200, PidVersion: 8, Euid: 501}
	evaluator.set(oldProc, Identity{
		TeamID:         "TEAMGOOD",
		SigningID:      "com.example.agent",
		PlatformBinary: false,
	})
	evaluator.set(reused, Identity{
		TeamID:         "EVILTEAM",
		SigningID:      "com.evil.implant",
		PlatformBinary: false,
	})

	check(t, true, validator.ValidateConnection(oldProc), nil, "original process connection")
	check(t, true, validator.Authorize(oldProc, "agent.read"), nil, "original process call")

	validator.NotifyExit(oldProc.Pid)

	// 新进程复用 pid=200，但 pidversion 不同，旧缓存既被退出事件清除，
	// 也无法用新令牌命中，必须实时判定出新的恶意身份。
	check(t, false, validator.Authorize(reused, "agent.read"), ErrPeerNotRegistered, "reused pid borrowing old grant")
	check(t, false, validator.ValidateConnection(reused), ErrPeerNotRegistered, "reused pid connection")
	if got := evaluator.count(reused); got != 1 {
		t.Fatalf("reused pid evaluations = %d, want 1 (no cache sharing across pidversion)", got)
	}

	// 即使没有退出事件，pidversion 不同也天然无法命中旧缓存。
	other := AuditToken{Pid: 300, PidVersion: 1, Euid: 501}
	otherV2 := AuditToken{Pid: 300, PidVersion: 2, Euid: 501}
	evaluator.set(other, Identity{TeamID: "TEAMGOOD", SigningID: "com.example.agent"})
	evaluator.set(otherV2, Identity{TeamID: "TEAMOTHER", SigningID: "com.example.other"})
	check(t, true, validator.Authorize(other, "agent.write"), nil, "other process original token")
	check(t, true, validator.Authorize(otherV2, "other.ping"), nil, "new pidversion evaluated to different identity")
	check(t, false, validator.Authorize(otherV2, "agent.write"), ErrUnauthorized, "new identity cannot use old auth key")

	// euid 变化也必须重新判定，不能仅靠 pid+pidversion 复用结论。
	setuid := AuditToken{Pid: 300, PidVersion: 2, Euid: 0}
	evaluator.set(setuid, Identity{TeamID: "TEAMGOOD", SigningID: "com.example.agent"})
	check(t, true, validator.Authorize(setuid, "agent.read"), nil, "distinct euid gets its own evaluation")
	if got := evaluator.count(setuid); got != 1 {
		t.Fatalf("euid-changed evaluations = %d, want 1", got)
	}
}

// TestTeamAndSigningID 覆盖团队 ID 与签名标识的联合匹配：
// 签名标识相同但团队不同必须拒绝；平台二进制标记不同同样不构成匹配。
func TestTeamAndSigningID(t *testing.T) {
	evaluator := newFakeEvaluator()
	validator := mustValidator(t, evaluator.eval, testGrants...)

	cases := []struct {
		name     string
		identity Identity
		wantErr  error
	}{
		{
			name:     "same signing id different team",
			identity: Identity{TeamID: "EVILTEAM", SigningID: "com.example.agent"},
			wantErr:  ErrPeerNotRegistered,
		},
		{
			name:     "same team different signing id",
			identity: Identity{TeamID: "TEAMGOOD", SigningID: "com.evil.implant"},
			wantErr:  ErrPeerNotRegistered,
		},
		{
			name:     "platform flag mismatch",
			identity: Identity{TeamID: "", SigningID: "com.apple.platform.syspolicy", PlatformBinary: false},
			wantErr:  ErrPeerNotRegistered,
		},
		{
			name:     "exact platform binary match",
			identity: Identity{TeamID: "", SigningID: "com.apple.platform.syspolicy", PlatformBinary: true},
			wantErr:  nil,
		},
	}

	for index, tc := range cases {
		token := AuditToken{Pid: 400 + index, PidVersion: 1, Euid: 501}
		evaluator.set(token, tc.identity)
		err := validator.ValidateConnection(token)
		if tc.wantErr == nil {
			check(t, true, err, nil, tc.name)
			check(t, true, validator.Authorize(token, "platform.policy"), nil, tc.name+" auth key")
			continue
		}
		check(t, false, err, tc.wantErr, tc.name)
	}
}

// TestLegitimateCalls 覆盖合法调用：登记身份可建立连接并仅调用其被授予的键，
// 同一令牌的后续消息复用判定结果但每次都重新核对授权范围。
func TestLegitimateCalls(t *testing.T) {
	evaluator := newFakeEvaluator()
	validator := mustValidator(t, evaluator.eval, testGrants...)

	agent := AuditToken{Pid: 500, PidVersion: 3, Euid: 501}
	other := AuditToken{Pid: 600, PidVersion: 1, Euid: 501}
	evaluator.set(agent, Identity{TeamID: "TEAMGOOD", SigningID: "com.example.agent"})
	evaluator.set(other, Identity{TeamID: "TEAMOTHER", SigningID: "com.example.other"})

	check(t, true, validator.ValidateConnection(agent), nil, "agent connection")
	check(t, true, validator.Authorize(agent, "agent.read"), nil, "agent read")
	check(t, true, validator.Authorize(agent, "agent.write"), nil, "agent write")
	check(t, false, validator.Authorize(agent, "other.ping"), ErrUnauthorized, "agent using foreign key")

	// 未显式建立连接的合法令牌可在消息路径上按需判定。
	check(t, true, validator.Authorize(other, "other.ping"), nil, "other peer on-demand evaluation")
	check(t, false, validator.Authorize(other, "agent.read"), ErrUnauthorized, "other peer scope enforced")

	if got := evaluator.count(agent); got != 1 {
		t.Fatalf("agent evaluations = %d, want 1", got)
	}
	if got := evaluator.count(other); got != 1 {
		t.Fatalf("other evaluations = %d, want 1", got)
	}

	// Invalidate 之后同一令牌必须重新执行代码要求判定。
	validator.Invalidate(agent)
	check(t, true, validator.Authorize(agent, "agent.read"), nil, "agent after invalidate")
	if got := evaluator.count(agent); got != 2 {
		t.Fatalf("agent evaluations after invalidate = %d, want 2", got)
	}
}

// TestEvaluatorFailure 覆盖判定器报错：不得放行，也不得缓存失败结论。
func TestEvaluatorFailure(t *testing.T) {
	evaluator := newFakeEvaluator()
	validator := mustValidator(t, evaluator.eval, testGrants...)
	token := AuditToken{Pid: 700, PidVersion: 1, Euid: 501}
	boom := errors.New("sec code check failed")
	evaluator.fail(token, boom)

	err := validator.ValidateConnection(token)
	if !errors.Is(err, boom) {
		t.Fatalf("ValidateConnection error = %v, want wrapped %v", err, boom)
	}
	err = validator.Authorize(token, "agent.read")
	if !errors.Is(err, boom) {
		t.Fatalf("Authorize error = %v, want wrapped %v", err, boom)
	}

	evaluator.clearError(token)
	evaluator.set(token, Identity{TeamID: "TEAMGOOD", SigningID: "com.example.agent"})
	check(t, true, validator.Authorize(token, "agent.read"), nil, "recovery after transient failure")
	if got := evaluator.count(token); got != 3 {
		t.Fatalf("evaluations = %d, want 3 (two failed attempts plus one success; failures are not cached)", got)
	}
}

// TestConcurrentAuthorize 验证高并发消息路径下缓存与登记表查找的线程安全。
func TestConcurrentAuthorize(t *testing.T) {
	evaluator := newFakeEvaluator()
	validator := mustValidator(t, evaluator.eval, testGrants...)
	token := AuditToken{Pid: 800, PidVersion: 1, Euid: 501}
	evaluator.set(token, Identity{TeamID: "TEAMGOOD", SigningID: "com.example.agent"})

	const goroutines = 64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := validator.Authorize(token, "agent.read"); err != nil {
				t.Errorf("concurrent Authorize error = %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	// 冷缓存下首批 goroutine 可能各自触发一次判定（竞争窗口），关键是
	// 所有调用都拿到同一结论且之后稳定命中缓存。
	first := atomic.LoadInt32(&evaluator.totalCnt)
	if first < 1 || first > goroutines {
		t.Fatalf("cold-cache evaluations = %d, want within [1,%d]", first, goroutines)
	}
	check(t, true, validator.Authorize(token, "agent.read"), nil, "post-race cached call")
	if got := atomic.LoadInt32(&evaluator.totalCnt); got != first {
		t.Fatalf("warm-cache evaluations = %d, want unchanged %d", got, first)
	}
}

func TestNewValidatorRejectsBadGrants(t *testing.T) {
	evaluator := newFakeEvaluator().eval
	cases := []struct {
		name   string
		grants []Grant
	}{
		{"nil evaluator", nil},
		{"empty signing id", []Grant{{SigningID: "", AuthKeys: []string{"k"}}}},
		{"no auth keys", []Grant{{SigningID: "com.example.x"}}},
		{"empty auth key", []Grant{{SigningID: "com.example.x", AuthKeys: []string{""}}}},
		{
			"duplicate grant",
			[]Grant{
				{SigningID: "com.example.x", AuthKeys: []string{"a"}},
				{SigningID: "com.example.x", AuthKeys: []string{"b"}},
			},
		},
	}
	for _, tc := range cases {
		var err error
		if tc.name == "nil evaluator" {
			_, err = NewValidator(nil, tc.grants...)
		} else {
			_, err = NewValidator(evaluator, tc.grants...)
		}
		if !errors.Is(err, ErrInvalidGrant) {
			t.Fatalf("%s: error = %v, want %v", tc.name, err, ErrInvalidGrant)
		}
	}
}
