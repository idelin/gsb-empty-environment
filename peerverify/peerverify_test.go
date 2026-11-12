package peerverify

import (
	"errors"
	"sync"
	"testing"
)

const (
	teamTrusted = "ABCD1234EF"
	teamRogue   = "ZZZZ9999ZZ"
	sigApp      = "com.example.agent"
	keyRead     = "config.read"
	keyWrite    = "config.write"
)

// fakeEvaluator 模拟基于审计令牌的代码要求判定。同一审计令牌在 exec 后
// 通过替换/新增映射返回新身份，模拟真实进程镜像已改变。
type fakeEvaluator struct {
	mu         sync.Mutex
	identities map[AuditToken]CodeIdentity
	fail       map[AuditToken]error
	evalCalls  int
}

func newFakeEvaluator() *fakeEvaluator {
	return &fakeEvaluator{
		identities: make(map[AuditToken]CodeIdentity),
		fail:       make(map[AuditToken]error),
	}
}

func (f *fakeEvaluator) setIdentity(token AuditToken, identity CodeIdentity) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.identities[token] = identity
}

func (f *fakeEvaluator) setFailure(token AuditToken, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail[token] = err
}

func (f *fakeEvaluator) EvaluateCode(token AuditToken) (CodeIdentity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.evalCalls++
	if err, ok := f.fail[token]; ok {
		return CodeIdentity{}, err
	}
	identity, ok := f.identities[token]
	if !ok {
		return CodeIdentity{}, errors.New("no code identity for audit token")
	}
	return identity, nil
}

func (f *fakeEvaluator) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.evalCalls
}

func newTestVerifier(t *testing.T, evaluator Evaluator) *Verifier {
	t.Helper()
	v, err := New(evaluator,
		Registration{
			TeamID:      teamTrusted,
			SigningID:   sigApp,
			AllowedKeys: []string{keyRead, keyWrite},
		},
		Registration{
			TeamID:                "",
			SigningID:             "com.apple.security.privileged-daemon",
			RequirePlatformBinary: true,
			AllowedKeys:           []string{"system.reboot"},
		},
	)
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}
	return v
}

// 场景一：通过检查后 exec 另一个二进制，旧结论必须作废并重新判定。
func TestPostCheckExec(t *testing.T) {
	evaluator := newFakeEvaluator()
	v := newTestVerifier(t, evaluator)

	token := AuditToken{PID: 100, PIDVersion: 1, EUID: 501}
	evaluator.setIdentity(token, CodeIdentity{TeamID: teamTrusted, SigningID: sigApp})

	if _, err := v.Connect(token); err != nil {
		t.Fatalf("Connect() on trusted identity: %v", err)
	}
	if err := v.Authorize(token, keyWrite); err != nil {
		t.Fatalf("Authorize() before exec: %v", err)
	}
	if calls := evaluator.callCount(); calls != 1 {
		t.Fatalf("expected 1 evaluation from Connect+Authorize, got %d", calls)
	}

	// 对端 exec 成未登记的恶意二进制；该系统事件携带同一审计令牌。
	rogueAfterExec := CodeIdentity{TeamID: teamRogue, SigningID: "com.attacker.payload"}
	evaluator.setIdentity(token, rogueAfterExec)
	v.OnExec(token)

	if v.hasDecision(token) {
		t.Fatal("cached decision must be invalidated after exec")
	}
	if err := v.Authorize(token, keyWrite); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("Authorize() after exec = %v, want ErrNotRegistered", err)
	}
	if calls := evaluator.callCount(); calls != 2 {
		t.Fatalf("expected forced re-evaluation (2 calls), got %d", calls)
	}

	// 恢复受信身份后放行；若同签名标识但团队 ID 不同，仍然拒绝。
	evaluator.setIdentity(token, CodeIdentity{TeamID: teamTrusted, SigningID: sigApp})
	v.OnExec(token)
	if err := v.Authorize(token, keyRead); err != nil {
		t.Fatalf("Authorize() restored trusted identity: %v", err)
	}
	evaluator.setIdentity(token, CodeIdentity{TeamID: teamRogue, SigningID: sigApp})
	v.OnExec(token)
	if err := v.Authorize(token, keyRead); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("same signing id under rogue team = %v, want ErrNotRegistered", err)
	}
}

// 场景二：pid 被复用，新进程实例不能借用旧进程已通过的检查。
func TestPIDReuse(t *testing.T) {
	evaluator := newFakeEvaluator()
	v := newTestVerifier(t, evaluator)

	oldToken := AuditToken{PID: 200, PIDVersion: 7, EUID: 501}
	evaluator.setIdentity(oldToken, CodeIdentity{TeamID: teamTrusted, SigningID: sigApp})
	if _, err := v.Connect(oldToken); err != nil {
		t.Fatalf("Connect() old process: %v", err)
	}

	// 旧进程退出，随后同一 pid 被新进程复用，pidversion 不同。
	v.OnExit(oldToken)
	if v.hasDecision(oldToken) {
		t.Fatal("old decision must be invalidated on exit")
	}

	reusedToken := AuditToken{PID: 200, PIDVersion: 8, EUID: 501}
	evaluator.setIdentity(reusedToken, CodeIdentity{TeamID: teamRogue, SigningID: "com.attacker.reuse"})

	if err := v.Authorize(reusedToken, keyWrite); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("reused pid with rogue identity = %v, want ErrNotRegistered", err)
	}

	// 旧审计令牌即便被重放，也必须重新判定且无法通过。
	evaluator.setIdentity(oldToken, CodeIdentity{TeamID: teamRogue, SigningID: "com.attacker.replay"})
	if err := v.Authorize(oldToken, keyWrite); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("replayed stale audit token = %v, want ErrNotRegistered", err)
	}

	// 新进程若身份合法，凭自己的审计令牌获得独立结论，不依赖旧结论。
	evaluator.setIdentity(reusedToken, CodeIdentity{TeamID: teamTrusted, SigningID: sigApp})
	if err := v.Authorize(reusedToken, keyRead); err != nil {
		t.Fatalf("legitimate reused process Authorize(): %v", err)
	}
}

// 场景三：签名标识相同但团队 ID 不同，必须拒绝；团队与标识须同时匹配。
func TestSameSigningIDDifferentTeam(t *testing.T) {
	evaluator := newFakeEvaluator()
	v := newTestVerifier(t, evaluator)

	token := AuditToken{PID: 300, PIDVersion: 1, EUID: 501}
	evaluator.setIdentity(token, CodeIdentity{TeamID: teamRogue, SigningID: sigApp})

	if _, err := v.Connect(token); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("Connect() = %v, want ErrNotRegistered", err)
	}
	if err := v.Authorize(token, keyRead); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("Authorize() = %v, want ErrNotRegistered", err)
	}
	if v.hasDecision(token) {
		t.Fatal("unregistered identity must not be cached as a decision")
	}

	// 修正为登记团队后立即放行，证明未缓存失败结果。
	evaluator.setIdentity(token, CodeIdentity{TeamID: teamTrusted, SigningID: sigApp})
	if err := v.Authorize(token, keyRead); err != nil {
		t.Fatalf("Authorize() after identity correction: %v", err)
	}

	// 平台二进制要求：非平台二进制不能命中要求平台二进制的登记项。
	platformSig := "com.apple.security.privileged-daemon"
	platformToken := AuditToken{PID: 301, PIDVersion: 1, EUID: 0}
	evaluator.setIdentity(platformToken, CodeIdentity{SigningID: platformSig, PlatformBinary: false})
	if err := v.Authorize(platformToken, "system.reboot"); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("non-platform binary = %v, want ErrNotRegistered", err)
	}
	evaluator.setIdentity(platformToken, CodeIdentity{SigningID: platformSig, PlatformBinary: true})
	if err := v.Authorize(platformToken, "system.reboot"); err != nil {
		t.Fatalf("platform binary Authorize(): %v", err)
	}
}

// 场景四：合法调用全链路，以及授权键按当前身份实时查询、不缓存连接期身份。
func TestLegitimateCallsAndLiveAuthorization(t *testing.T) {
	evaluator := newFakeEvaluator()
	v := newTestVerifier(t, evaluator)

	token := AuditToken{PID: 400, PIDVersion: 1, EUID: 501}
	evaluator.setIdentity(token, CodeIdentity{TeamID: teamTrusted, SigningID: sigApp})

	if _, err := v.Connect(token); err != nil {
		t.Fatalf("Connect(): %v", err)
	}
	if err := v.Authorize(token, keyRead); err != nil {
		t.Fatalf("Authorize(read): %v", err)
	}
	if err := v.Authorize(token); err != nil {
		t.Fatalf("Authorize() without required keys: %v", err)
	}

	// 未授予的授权键即使在已通过的连接上也必须拒绝。
	if err := v.Authorize(token, "kernel.load"); !errors.Is(err, ErrUnauthorizedKey) {
		t.Fatalf("Authorize(ungranted key) = %v, want ErrUnauthorizedKey", err)
	}

	before := evaluator.callCount()

	// 运行期撤销一个授权键：不产生 exec/退出事件，也必须立即生效，
	// 且授权查询不得触发新的代码判定（身份结论仍由审计令牌绑定）。
	if err := v.UpsertRegistration(Registration{
		TeamID:      teamTrusted,
		SigningID:   sigApp,
		AllowedKeys: []string{keyRead},
	}); err != nil {
		t.Fatalf("UpsertRegistration(): %v", err)
	}
	if err := v.Authorize(token, keyWrite); !errors.Is(err, ErrUnauthorizedKey) {
		t.Fatalf("revoked key still accepted: %v", err)
	}
	if err := v.Authorize(token, keyRead); err != nil {
		t.Fatalf("remaining key rejected: %v", err)
	}

	// 删除整个登记项后，已建立连接上的后续消息立即失去授权。
	v.RemoveRegistration(teamTrusted, sigApp)
	if err := v.Authorize(token, keyRead); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("removed registration still accepted: %v", err)
	}

	if calls := evaluator.callCount(); calls != before {
		t.Fatalf("authorization changes must not re-evaluate code: calls before=%d after=%d", before, calls)
	}
}

// 场景五：pidversion 变化（同 pid 的新审计令牌）强制重新判定。
func TestPIDVersionChangeForcesReevaluation(t *testing.T) {
	evaluator := newFakeEvaluator()
	v := newTestVerifier(t, evaluator)

	versionOne := AuditToken{PID: 500, PIDVersion: 1, EUID: 501}
	versionTwo := AuditToken{PID: 500, PIDVersion: 2, EUID: 501}
	evaluator.setIdentity(versionOne, CodeIdentity{TeamID: teamTrusted, SigningID: sigApp})

	if _, err := v.Connect(versionOne); err != nil {
		t.Fatalf("Connect(version 1): %v", err)
	}

	// 新 pidversion 是不同的键，且当前身份为未登记身份。
	evaluator.setIdentity(versionTwo, CodeIdentity{TeamID: teamRogue, SigningID: sigApp})
	if err := v.Authorize(versionTwo, keyRead); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("new pidversion = %v, want ErrNotRegistered", err)
	}

	// euid 变化同样构成不同审计令牌，不能命中旧结论。
	otherEUID := AuditToken{PID: 500, PIDVersion: 1, EUID: 0}
	evaluator.setIdentity(otherEUID, CodeIdentity{TeamID: teamRogue, SigningID: sigApp})
	if err := v.Authorize(otherEUID, keyRead); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("changed euid = %v, want ErrNotRegistered", err)
	}
}

func TestEvaluatorFailureIsNotCached(t *testing.T) {
	evaluator := newFakeEvaluator()
	v := newTestVerifier(t, evaluator)

	token := AuditToken{PID: 600, PIDVersion: 1, EUID: 501}
	evalErr := errors.New("SecCodeCheckValidity failed")
	evaluator.setFailure(token, evalErr)

	err := v.Authorize(token, keyRead)
	var wrapped *EvaluatorError
	if !errors.As(err, &wrapped) || !errors.Is(err, evalErr) {
		t.Fatalf("Authorize() = %v, want wrapped EvaluatorError", err)
	}
	if v.hasDecision(token) {
		t.Fatal("evaluator failure must not be cached")
	}

	delete(evaluator.fail, token)
	evaluator.setIdentity(token, CodeIdentity{TeamID: teamTrusted, SigningID: sigApp})
	if err := v.Authorize(token, keyRead); err != nil {
		t.Fatalf("retry after transient failure: %v", err)
	}
}

func TestNewRejectsInvalidInput(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("New(nil) must return an error")
	}
	evaluator := newFakeEvaluator()
	if _, err := New(evaluator, Registration{SigningID: ""}); err == nil {
		t.Fatal("registration without signing id must be rejected")
	}
	if _, err := New(evaluator, Registration{
		SigningID:   sigApp,
		AllowedKeys: []string{""},
	}); err == nil {
		t.Fatal("empty authorization key must be rejected")
	}
}
