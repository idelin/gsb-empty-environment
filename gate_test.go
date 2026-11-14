package netsec

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testClient = "client-7f3a"
	testSNI    = "pay.example.com"
	testALPN   = "h3"
)

func newTestGate(t *testing.T) (*Issuer, *Gate) {
	t.Helper()
	issuer := NewIssuer(time.Minute)
	gate := NewGate(issuer, DefaultPolicy())
	return issuer, gate
}

func issueToken(t *testing.T, issuer *Issuer, gate *Gate, clientID, sni string) string {
	t.Helper()
	token, _, err := issuer.Issue(clientID, sni, testALPN, gate.Policy().Version)
	if err != nil {
		t.Fatalf("issue ticket: %v", err)
	}
	return token
}

// 支付POST重放:携带一次性幂等键的支付请求被离线重放时,
// 重放缓存命中并共享首次结果,扣款副作用只发生一次。
func TestPaymentPostReplayChargedOnce(t *testing.T) {
	issuer, gate := newTestGate(t)
	token := issueToken(t, issuer, gate, testClient, testSNI)
	req := Request{Method: "POST", SNI: testSNI, ALPN: testALPN, IdempotencyKey: "pay-2026-0001"}

	var charges atomic.Int32
	charge := func() (any, error) {
		charges.Add(1)
		return "receipt-0001", nil
	}

	adm := gate.AdmitEarly(token, req)
	if adm.Decision != DecisionAllowEarly {
		t.Fatalf("first request: got %v (%s), want allow-early", adm.Decision, adm.Reason)
	}
	res, shared, err := gate.Execute(adm, charge)
	if err != nil || shared {
		t.Fatalf("first execute: res=%v shared=%v err=%v", res, shared, err)
	}

	// 攻击者离线重放同一张票据与同一个幂等键。
	replay := gate.AdmitEarly(token, req)
	if replay.Decision != DecisionAllowEarly {
		t.Fatalf("replay admission: got %v (%s)", replay.Decision, replay.Reason)
	}
	res2, shared2, err := gate.Execute(replay, charge)
	if err != nil {
		t.Fatalf("replay execute: %v", err)
	}
	if !shared2 {
		t.Fatal("replay was not absorbed by the replay cache")
	}
	if res2 != "receipt-0001" {
		t.Fatalf("replay got %v, want the original receipt", res2)
	}
	if got := charges.Load(); got != 1 {
		t.Fatalf("charge side effect ran %d times, want exactly 1", got)
	}
}

// 幂等键缺失:非幂等方法且没有幂等键时不得在首航班执行,
// 必须推迟到握手完成之后。
func TestPostWithoutIdempotencyKeyDeferred(t *testing.T) {
	issuer, gate := newTestGate(t)
	token := issueToken(t, issuer, gate, testClient, testSNI)
	req := Request{Method: "POST", SNI: testSNI, ALPN: testALPN}

	var charges atomic.Int32
	charge := func() (any, error) {
		charges.Add(1)
		return "receipt", nil
	}

	adm := gate.AdmitEarly(token, req)
	if adm.Decision != DecisionDefer {
		t.Fatalf("got %v, want defer-until-handshake", adm.Decision)
	}
	if _, _, err := gate.Execute(adm, charge); err == nil {
		t.Fatal("deferred admission must refuse early execution")
	}
	if got := charges.Load(); got != 0 {
		t.Fatalf("handler ran %d times before handshake, want 0", got)
	}

	// 握手完成后放行,且只执行一次。
	post := gate.AdmitPostHandshake(token, req)
	if post.Decision != DecisionAllowEarly {
		t.Fatalf("post-handshake: got %v (%s)", post.Decision, post.Reason)
	}
	if _, _, err := gate.Execute(post, charge); err != nil {
		t.Fatalf("post-handshake execute: %v", err)
	}
	if got := charges.Load(); got != 1 {
		t.Fatalf("handler ran %d times after handshake, want 1", got)
	}
}

// 跨SNI复用:票据绑定签发时的SNI,换个站点呈现必须拒绝。
func TestCrossSNIReuseRejected(t *testing.T) {
	issuer, gate := newTestGate(t)
	token := issueToken(t, issuer, gate, testClient, testSNI)

	req := Request{Method: "GET", SNI: "evil.example.net", ALPN: testALPN}
	adm := gate.AdmitEarly(token, req)
	if adm.Decision != DecisionReject {
		t.Fatalf("cross-SNI reuse: got %v, want reject", adm.Decision)
	}

	// ALPN 不一致同样拒绝。
	req = Request{Method: "GET", SNI: testSNI, ALPN: "h2"}
	if adm := gate.AdmitEarly(token, req); adm.Decision != DecisionReject {
		t.Fatalf("cross-ALPN reuse: got %v, want reject", adm.Decision)
	}
}

// 票据撤销、轮换与策略收紧都会让旧票据立即失效。
func TestTicketInvalidation(t *testing.T) {
	t.Run("revoke client", func(t *testing.T) {
		issuer, gate := newTestGate(t)
		token := issueToken(t, issuer, gate, testClient, testSNI)
		req := Request{Method: "GET", SNI: testSNI, ALPN: testALPN}
		if adm := gate.AdmitEarly(token, req); adm.Decision != DecisionAllowEarly {
			t.Fatalf("pre-revocation: got %v", adm.Decision)
		}
		issuer.RevokeClient(testClient)
		adm := gate.AdmitEarly(token, req)
		if adm.Decision != DecisionReject || adm.Reason != ErrTicketRevoked.Error() {
			t.Fatalf("post-revocation: got %v (%s), want reject/revoked", adm.Decision, adm.Reason)
		}
	})

	t.Run("revoke session", func(t *testing.T) {
		issuer, gate := newTestGate(t)
		token, ticket, err := issuer.Issue(testClient, testSNI, testALPN, gate.Policy().Version)
		if err != nil {
			t.Fatal(err)
		}
		issuer.RevokeSession(ticket.SessionID)
		req := Request{Method: "GET", SNI: testSNI, ALPN: testALPN}
		if adm := gate.AdmitEarly(token, req); adm.Decision != DecisionReject {
			t.Fatalf("revoked session: got %v, want reject", adm.Decision)
		}
	})

	t.Run("rotation", func(t *testing.T) {
		issuer, gate := newTestGate(t)
		token := issueToken(t, issuer, gate, testClient, testSNI)
		issuer.Rotate()
		req := Request{Method: "GET", SNI: testSNI, ALPN: testALPN}
		if adm := gate.AdmitEarly(token, req); adm.Decision != DecisionReject {
			t.Fatalf("stale generation: got %v, want reject", adm.Decision)
		}
	})

	t.Run("policy tightening", func(t *testing.T) {
		issuer, gate := newTestGate(t)
		token := issueToken(t, issuer, gate, testClient, testSNI)
		gate.Tighten(func(p *Policy) { p.AllowIdempotentEarly = false })
		req := Request{Method: "GET", SNI: testSNI, ALPN: testALPN}
		if adm := gate.AdmitEarly(token, req); adm.Decision != DecisionReject {
			t.Fatalf("superseded policy: got %v, want reject", adm.Decision)
		}
		// 新策略下签发的票据仍然可用。
		fresh := issueToken(t, issuer, gate, testClient, testSNI)
		keyed := Request{Method: "POST", SNI: testSNI, ALPN: testALPN, IdempotencyKey: "k1"}
		if adm := gate.AdmitEarly(fresh, keyed); adm.Decision != DecisionAllowEarly {
			t.Fatalf("fresh ticket under tightened policy: got %v (%s)", adm.Decision, adm.Reason)
		}
	})
}

// 并发重放:攻击者同时重放大量相同请求,处理器只执行一次,
// 所有调用者拿到同一份正式结果。
func TestConcurrentReplayExecutesOnce(t *testing.T) {
	issuer, gate := newTestGate(t)
	token := issueToken(t, issuer, gate, testClient, testSNI)
	req := Request{Method: "POST", SNI: testSNI, ALPN: testALPN, IdempotencyKey: "pay-race"}

	var charges atomic.Int32
	charge := func() (any, error) {
		charges.Add(1)
		time.Sleep(20 * time.Millisecond) // 拉宽并发窗口
		return "receipt-race", nil
	}

	const attackers = 32
	start := make(chan struct{})
	results := make([]any, attackers)
	shared := make([]bool, attackers)
	errs := make([]error, attackers)
	var wg sync.WaitGroup
	for i := 0; i < attackers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			adm := gate.AdmitEarly(token, req)
			if adm.Decision != DecisionAllowEarly {
				errs[idx] = errors.New("unexpected decision: " + adm.Decision.String())
				return
			}
			results[idx], shared[idx], errs[idx] = gate.Execute(adm, charge)
		}(i)
	}
	close(start)
	wg.Wait()

	if got := charges.Load(); got != 1 {
		t.Fatalf("handler executed %d times under concurrent replay, want 1", got)
	}
	sharedCount := 0
	for i := 0; i < attackers; i++ {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		if results[i] != "receipt-race" {
			t.Fatalf("caller %d got %v, want the shared receipt", i, results[i])
		}
		if shared[i] {
			sharedCount++
		}
	}
	if sharedCount != attackers-1 {
		t.Fatalf("%d callers shared the result, want %d", sharedCount, attackers-1)
	}
}

// 合法GET:幂等方法在首航班直接放行,副作用最多一次。
func TestLegitimateGETAllowedEarly(t *testing.T) {
	issuer, gate := newTestGate(t)
	token := issueToken(t, issuer, gate, testClient, testSNI)
	req := Request{Method: "GET", SNI: testSNI, ALPN: testALPN}

	var hits atomic.Int32
	handler := func() (any, error) {
		hits.Add(1)
		return "balance: 42", nil
	}

	adm := gate.AdmitEarly(token, req)
	if adm.Decision != DecisionAllowEarly {
		t.Fatalf("legitimate GET: got %v (%s), want allow-early", adm.Decision, adm.Reason)
	}
	res, shared, err := gate.Execute(adm, handler)
	if err != nil || shared {
		t.Fatalf("execute: res=%v shared=%v err=%v", res, shared, err)
	}
	if res != "balance: 42" {
		t.Fatalf("got %v", res)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("GET side effect ran %d times, want 1", got)
	}
}

// 重放缓存按身份隔离:同一幂等键属于不同客户端时互不干扰。
func TestReplayCacheScopedByIdentity(t *testing.T) {
	issuer, gate := newTestGate(t)
	tokenA := issueToken(t, issuer, gate, "client-a", testSNI)
	tokenB := issueToken(t, issuer, gate, "client-b", testSNI)
	req := Request{Method: "POST", SNI: testSNI, ALPN: testALPN, IdempotencyKey: "shared-key"}

	var charges atomic.Int32
	charge := func() (any, error) {
		n := charges.Add(1)
		return n, nil
	}

	admA := gate.AdmitEarly(tokenA, req)
	admB := gate.AdmitEarly(tokenB, req)
	if _, sharedA, _ := gate.Execute(admA, charge); sharedA {
		t.Fatal("client A first execution must not be shared")
	}
	if _, sharedB, _ := gate.Execute(admB, charge); sharedB {
		t.Fatal("client B must not share client A's result")
	}
	if got := charges.Load(); got != 2 {
		t.Fatalf("handler ran %d times, want 2 (one per identity)", got)
	}
}
