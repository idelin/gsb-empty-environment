package netsec

import (
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	legitClient    = "client-legit"
	attackerClient = "client-attacker"
	userSession    = "sess-user-123"
)

func authParams() url.Values {
	return url.Values{
		"response_type": {"code"},
		"client_id":     {legitClient},
		"redirect_uri":  {"https://legit.example/callback"},
		"scope":         {"openid profile"},
		"state":         {"state-abc"},
		"nonce":         {"nonce-xyz"},
	}
}

func push(t *testing.T, s *Store) (string, url.Values) {
	t.Helper()
	params := authParams()
	uri, err := s.Push(legitClient, userSession, params)
	if err != nil {
		t.Fatalf("Push failed: %v", err)
	}
	if !strings.HasPrefix(uri, RequestURIScheme) {
		t.Fatalf("request_uri %q missing URN scheme prefix", uri)
	}
	return uri, params
}

// 合法兑换：同 client、同会话、同参数，成功且返回原请求内容。
func TestLegitimateRedemption(t *testing.T) {
	s := NewStore()
	uri, params := push(t, s)
	obj, err := s.Redeem(legitClient, userSession, uri, params)
	if err != nil {
		t.Fatalf("legitimate redeem failed: %v", err)
	}
	if obj.ClientID() != legitClient {
		t.Errorf("ClientID = %q, want %q", obj.ClientID(), legitClient)
	}
	if obj.SessionID() != userSession {
		t.Errorf("SessionID = %q, want %q", obj.SessionID(), userSession)
	}
	if obj.Scope() != "openid profile" {
		t.Errorf("Scope = %q, want %q", obj.Scope(), "openid profile")
	}
	if obj.RedirectURI() != "https://legit.example/callback" {
		t.Errorf("RedirectURI = %q", obj.RedirectURI())
	}
	if got := obj.Params().Get("state"); got != "state-abc" {
		t.Errorf("Params state = %q", got)
	}
}

// 跨 client 兑换：攻击者拿到 request_uri，用自己的 client 兑换必须失败。
func TestCrossClientRedemption(t *testing.T) {
	s := NewStore()
	uri, _ := push(t, s)
	attackerParams := authParams()
	attackerParams.Set("client_id", attackerClient)
	_, err := s.Redeem(attackerClient, userSession, uri, attackerParams)
	if !errors.Is(err, ErrClientMismatch) {
		t.Fatalf("cross-client redeem err = %v, want ErrClientMismatch", err)
	}
}

// 跨会话兑换：同 client 但用户会话不同，必须失败。
func TestCrossSessionRedemption(t *testing.T) {
	s := NewStore()
	uri, params := push(t, s)
	_, err := s.Redeem(legitClient, "sess-attacker", uri, params)
	if !errors.Is(err, ErrSessionMismatch) {
		t.Fatalf("cross-session redeem err = %v, want ErrSessionMismatch", err)
	}
}

// 参数篡改：攻击者保留原 client，但篡改 redirect_uri 指向自己。
func TestRedirectURITampering(t *testing.T) {
	s := NewStore()
	uri, _ := push(t, s)
	tampered := authParams()
	tampered.Set("redirect_uri", "https://attacker.example/callback")
	_, err := s.Redeem(legitClient, userSession, uri, tampered)
	if !errors.Is(err, ErrRedirectMismatch) {
		t.Fatalf("redirect tamper err = %v, want ErrRedirectMismatch", err)
	}
}

// 参数篡改：提权 scope。
func TestScopeTampering(t *testing.T) {
	s := NewStore()
	uri, _ := push(t, s)
	tampered := authParams()
	tampered.Set("scope", "openid profile admin")
	_, err := s.Redeem(legitClient, userSession, uri, tampered)
	if !errors.Is(err, ErrScopeMismatch) {
		t.Fatalf("scope tamper err = %v, want ErrScopeMismatch", err)
	}
}

// 参数篡改：篡改其他参数（state）同样被拒绝。
func TestGenericParamTampering(t *testing.T) {
	s := NewStore()
	uri, _ := push(t, s)
	tampered := authParams()
	tampered.Set("state", "forged-state")
	_, err := s.Redeem(legitClient, userSession, uri, tampered)
	if !errors.Is(err, ErrParamMismatch) {
		t.Fatalf("state tamper err = %v, want ErrParamMismatch", err)
	}
}

// 重放：兑换成功后同一 request_uri 不可再次使用。
func TestReplayRejected(t *testing.T) {
	s := NewStore()
	uri, params := push(t, s)
	if _, err := s.Redeem(legitClient, userSession, uri, params); err != nil {
		t.Fatalf("first redeem failed: %v", err)
	}
	_, err := s.Redeem(legitClient, userSession, uri, params)
	if !errors.Is(err, ErrRequestNotFound) {
		t.Fatalf("replay err = %v, want ErrRequestNotFound", err)
	}
}

// 并发兑换：N 个 goroutine 同时兑换同一 request_uri，只有一个成功。
func TestConcurrentRedemptionOnlyOneSucceeds(t *testing.T) {
	s := NewStore()
	uri, params := push(t, s)
	const goroutines = 64
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes := 0
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Redeem(legitClient, userSession, uri, params); err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("concurrent redeem successes = %d, want exactly 1", successes)
	}
}

// 过期：超过有效期后兑换被拒绝。
func TestExpiredRequestRejected(t *testing.T) {
	s := NewStore(WithTTL(50 * time.Millisecond))
	uri, params := push(t, s)
	time.Sleep(80 * time.Millisecond)
	_, err := s.Redeem(legitClient, userSession, uri, params)
	if !errors.Is(err, ErrRequestExpired) {
		t.Fatalf("expired redeem err = %v, want ErrRequestExpired", err)
	}
}

// 不可变性：推送后调用方修改原参数 map，不影响已存储的请求对象。
func TestPushFreezesParams(t *testing.T) {
	s := NewStore()
	params := authParams()
	uri, err := s.Push(legitClient, userSession, params)
	if err != nil {
		t.Fatalf("Push failed: %v", err)
	}
	// 推送后篡改调用方持有的参数。
	params.Set("redirect_uri", "https://attacker.example/callback")
	params.Set("scope", "admin")
	obj, err := s.Redeem(legitClient, userSession, uri, authParams())
	if err != nil {
		t.Fatalf("redeem with original params failed: %v", err)
	}
	if obj.RedirectURI() != "https://legit.example/callback" {
		t.Errorf("stored RedirectURI mutated to %q", obj.RedirectURI())
	}
	if obj.Scope() != "openid profile" {
		t.Errorf("stored Scope mutated to %q", obj.Scope())
	}
}

// 不可变性：修改兑换返回的参数副本，不影响存储对象。
func TestReturnedParamsAreCopies(t *testing.T) {
	s := NewStore()
	uri, params := push(t, s)
	obj, err := s.Redeem(legitClient, userSession, uri, params)
	if err != nil {
		t.Fatalf("redeem failed: %v", err)
	}
	mutated := obj.Params()
	mutated.Set("scope", "admin")
	if obj.Params().Get("scope") != "openid profile" {
		t.Errorf("returned params share state with stored object")
	}
}

// 请求对象只属于原 client：同一存储中其他 client 的对象互不可见。
func TestRequestObjectBoundToOriginalClient(t *testing.T) {
	s := NewStore()
	uriA, paramsA := push(t, s)
	// 另一 client 推送自己的请求，得到不同 request_uri。
	paramsB := authParams()
	paramsB.Set("client_id", attackerClient)
	uriB, err := s.Push(attackerClient, userSession, paramsB)
	if err != nil {
		t.Fatalf("Push B failed: %v", err)
	}
	if uriA == uriB {
		t.Fatal("distinct pushes must yield distinct request_uri")
	}
	// 攻击者用受害者的 request_uri 兑换：失败。
	if _, err := s.Redeem(attackerClient, userSession, uriA, paramsB); !errors.Is(err, ErrClientMismatch) {
		t.Fatalf("attacker redeeming victim uri err = %v, want ErrClientMismatch", err)
	}
	// 受害者用攻击者的 request_uri 兑换：同样失败。
	if _, err := s.Redeem(legitClient, userSession, uriB, paramsA); !errors.Is(err, ErrClientMismatch) {
		t.Fatalf("victim redeeming attacker uri err = %v, want ErrClientMismatch", err)
	}
	// 各自合法兑换自己的对象：成功。
	if _, err := s.Redeem(attackerClient, userSession, uriB, paramsB); err != nil {
		t.Fatalf("attacker legitimate redeem failed: %v", err)
	}
}

// 未知 request_uri 直接拒绝。
func TestUnknownRequestURI(t *testing.T) {
	s := NewStore()
	_, err := s.Redeem(legitClient, userSession, RequestURIScheme+"nonexistent", authParams())
	if !errors.Is(err, ErrRequestNotFound) {
		t.Fatalf("unknown uri err = %v, want ErrRequestNotFound", err)
	}
}
