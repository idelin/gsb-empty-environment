package netsec

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
)

// testHarness 持有两个源站与两个代理，全部本地回环，测试不依赖外部网络。
type testHarness struct {
	network *memoryNetwork

	originA *originSpec
	originB *originSpec
	proxy1  *proxySpec
	proxy2  *proxySpec

	originASrv *testServer
	originBSrv *testServer
	proxy1Srv  *testServer
	proxy2Srv  *testServer
}

func newTestHarness(t *testing.T) *testHarness {
	t.Helper()

	network := newMemoryNetwork()
	originA := &originSpec{realm: "realm-a", basicUser: "alice", basicPass: "pass-a"}
	originB := &originSpec{realm: "realm-b", basicUser: "bob", basicPass: "pass-b"}
	originASrv := newOriginServer(t, network, "origin-a.test:80", originA)
	originBSrv := newOriginServer(t, network, "origin-b.test:80", originB)
	originA.redirectTo = originBSrv.URL + "/landing"

	proxy1 := &proxySpec{realm: "proxy-one", user: "p1", pass: "pw1"}
	proxy2 := &proxySpec{realm: "proxy-two", user: "p2", pass: "pw2"}
	proxy1Srv := newProxyServer(t, network, "proxy-one.test:8080", proxy1)
	proxy2Srv := newProxyServer(t, network, "proxy-two.test:8080", proxy2)

	return &testHarness{
		network:    network,
		originA:    originA,
		originB:    originB,
		proxy1:     proxy1,
		proxy2:     proxy2,
		originASrv: originASrv,
		originBSrv: originBSrv,
		proxy1Srv:  proxy1Srv,
		proxy2Srv:  proxy2Srv,
	}
}

func getBody(t *testing.T, client *http.Client, target string) (int, string) {
	t.Helper()
	resp, err := client.Get(target)
	if err != nil {
		t.Fatalf("GET %s: %v", target, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// newMemoryTransport 构建走内存网络的真实 http.Transport，并关闭压缩等
// 与本测试无关的行为；连接池参数保持默认，以真实验证复用不扩权。
func newMemoryTransport(network *memoryNetwork) *http.Transport {
	return &http.Transport{
		DialContext:        network.dialContext,
		DisableCompression: true,
		MaxIdleConns:       16,
		IdleConnTimeout:    0,
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

func originIDOf(t *testing.T, raw string) Identity {
	t.Helper()
	return identityOf(mustURL(t, raw))
}

func proxyURLOf(t *testing.T, srv *testServer) *url.URL {
	t.Helper()
	return mustURL(t, srv.URL)
}

// 1. 基础 Basic 流程：首个请求无首部 -> 401 -> 受限重试 -> 200，
// 且第二次请求不再触发挑战（会话按身份缓存）。
func TestBasicOriginChallengeAndRetry(t *testing.T) {
	h := newTestHarness(t)

	creds := NewStaticCredentials()
	creds.Add(originIDOf(t, h.originASrv.URL), Credential{Username: "alice", Password: "pass-a"})

	tr := NewAuthTransport(AuthTransportConfig{Origins: creds, Base: newMemoryTransport(h.network)})
	client := tr.Client()

	status, body := getBody(t, client, h.originASrv.URL+"/resource")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", status, body)
	}

	records := h.originA.rec.snapshot()
	if len(records) != 2 {
		t.Fatalf("origin requests = %d, want 2 (bare then retried)", len(records))
	}
	if records[0].Authorization != "" {
		t.Fatalf("first request must carry no Authorization, got %q", records[0].Authorization)
	}
	if !strings.HasPrefix(records[1].Authorization, "Basic ") {
		t.Fatalf("retry must carry Basic Authorization, got %q", records[1].Authorization)
	}

	// 第二个请求应直接带缓存凭据，服务器只看到一次请求。
	status, _ = getBody(t, client, h.originASrv.URL+"/resource2")
	if status != http.StatusOK {
		t.Fatalf("second call status = %d, want 200", status)
	}
	records = h.originA.rec.snapshot()
	if len(records) != 3 {
		t.Fatalf("total origin requests = %d, want 3", len(records))
	}
	if records[2].Authorization == "" {
		t.Fatalf("cached session must be sent proactively")
	}
}

// 2. 重定向跨源站：A 的凭据绝不能出现在 B 的请求里；每跳重新评估。
func TestRedirectReevaluatesOriginOnly(t *testing.T) {
	h := newTestHarness(t)

	creds := NewStaticCredentials()
	creds.Add(originIDOf(t, h.originASrv.URL), Credential{Username: "alice", Password: "pass-a"})
	creds.Add(originIDOf(t, h.originBSrv.URL), Credential{Username: "bob", Password: "pass-b"})

	tr := NewAuthTransport(AuthTransportConfig{Origins: creds, Base: newMemoryTransport(h.network)})
	client := tr.Client()

	status, _ := getBody(t, client, h.originASrv.URL+"/redirect")
	if status != http.StatusOK {
		t.Fatalf("redirect chain status = %d, want 200", status)
	}

	wantA := "Basic " + basicToken("alice", "pass-a")
	wantB := "Basic " + basicToken("bob", "pass-b")
	for i, rec := range h.originA.rec.snapshot() {
		if rec.Authorization != "" && rec.Authorization != wantA {
			t.Fatalf("origin A request %d leaked foreign credential: %q", i, rec.Authorization)
		}
	}
	for i, rec := range h.originB.rec.snapshot() {
		if rec.Authorization == wantA {
			t.Fatalf("origin B request %d received A's credential: %q", i, rec.Authorization)
		}
		if rec.Authorization != "" && rec.Authorization != wantB {
			t.Fatalf("origin B request %d got unexpected credential: %q", i, rec.Authorization)
		}
	}
	if len(h.originB.rec.snapshot()) == 0 {
		t.Fatalf("origin B must receive the redirected request")
	}
}

// 3. 重定向目标身份不受信：不得发送任何凭据，最终把 401 交还调用方。
func TestRedirectToUntrustedOriginSendsNothing(t *testing.T) {
	h := newTestHarness(t)

	creds := NewStaticCredentials()
	creds.Add(originIDOf(t, h.originASrv.URL), Credential{Username: "alice", Password: "pass-a"})
	// 故意不登记 originB。

	tr := NewAuthTransport(AuthTransportConfig{Origins: creds, Base: newMemoryTransport(h.network)})
	client := tr.Client()

	status, _ := getBody(t, client, h.originASrv.URL+"/redirect")
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 for untrusted redirect target", status)
	}

	for i, rec := range h.originB.rec.snapshot() {
		if rec.Authorization != "" {
			t.Fatalf("untrusted origin B request %d must have empty Authorization, got %q", i, rec.Authorization)
		}
	}
}

// 4. 代理 407 挑战：代理先收到裸请求再收到 Proxy-Authorization；
// 源站收到的请求里绝不能出现代理凭据。
func TestProxyChallengePartitionsFromOrigin(t *testing.T) {
	h := newTestHarness(t)

	originCreds := NewStaticCredentials()
	originCreds.Add(originIDOf(t, h.originASrv.URL), Credential{Username: "alice", Password: "pass-a"})
	proxyCreds := NewStaticCredentials()
	proxyCreds.Add(originIDOf(t, h.proxy1Srv.URL), Credential{Username: "p1", Password: "pw1"})

	pu := proxyURLOf(t, h.proxy1Srv)
	tr := NewAuthTransport(AuthTransportConfig{
		Origins: originCreds,
		Proxies: proxyCreds,
		Base:    newMemoryTransport(h.network),
		Proxy:   func(*http.Request) (*url.URL, error) { return pu, nil },
	})

	status, _ := getBody(t, tr.Client(), h.originASrv.URL+"/via-proxy")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200 through authenticated proxy", status)
	}

	proxyRecords := h.proxy1.rec.snapshot()
	// 第一次裸请求触发 407；之后代理凭据缓存，源站自身的 401 重试
	// 复用同一条代理连接，因此代理共看到三次请求。
	if len(proxyRecords) != 3 {
		t.Fatalf("proxy requests = %d, want 3", len(proxyRecords))
	}
	if proxyRecords[0].ProxyAuthorization != "" {
		t.Fatalf("first proxy request must be bare, got %q", proxyRecords[0].ProxyAuthorization)
	}
	wantProxy := "Basic " + basicToken("p1", "pw1")
	for i := 1; i < len(proxyRecords); i++ {
		if proxyRecords[i].ProxyAuthorization != wantProxy {
			t.Fatalf("proxy request %d credential mismatch: %q", i, proxyRecords[i].ProxyAuthorization)
		}
		// 代理记录到的是转发前请求，Authorization 是源站角色的首部，
		// 绝不能与 Proxy-Authorization 混淆。
		if proxyRecords[i].Authorization == wantProxy {
			t.Fatalf("proxy request %d must not treat proxy credential as origin credential", i)
		}
	}

	// 代理在转发前剥离 Proxy-Authorization，源站永远看不到代理口令。
	for i, rec := range h.originA.rec.snapshot() {
		if rec.ProxyAuthorization != "" {
			t.Fatalf("origin A request %d saw proxy credential: %q", i, rec.ProxyAuthorization)
		}
	}
	// 源站自身的 401 流程同样完成，且只看到自己的 Basic 凭据。
	last := h.originA.rec.snapshot()[len(h.originA.rec.snapshot())-1]
	wantOrigin := "Basic " + basicToken("alice", "pass-a")
	if last.Authorization != wantOrigin {
		t.Fatalf("origin credential not delivered through proxy: %q", last.Authorization)
	}
}

// 4b. 代理 + 跨源站重定向组合：重定向只重新评估源站凭据，
// 代理凭据对整条重定向链保持有效且始终不落到源站。
func TestProxySurvivesRedirectWhileOriginReevaluates(t *testing.T) {
	h := newTestHarness(t)

	originCreds := NewStaticCredentials()
	originCreds.Add(originIDOf(t, h.originASrv.URL), Credential{Username: "alice", Password: "pass-a"})
	originCreds.Add(originIDOf(t, h.originBSrv.URL), Credential{Username: "bob", Password: "pass-b"})
	proxyCreds := NewStaticCredentials()
	proxyCreds.Add(originIDOf(t, h.proxy1Srv.URL), Credential{Username: "p1", Password: "pw1"})

	pu := proxyURLOf(t, h.proxy1Srv)
	tr := NewAuthTransport(AuthTransportConfig{
		Origins: originCreds,
		Proxies: proxyCreds,
		Base:    newMemoryTransport(h.network),
		Proxy:   func(*http.Request) (*url.URL, error) { return pu, nil },
	})

	status, _ := getBody(t, tr.Client(), h.originASrv.URL+"/redirect")
	if status != http.StatusOK {
		t.Fatalf("redirect through proxy status = %d, want 200", status)
	}

	wantProxy := "Basic " + basicToken("p1", "pw1")
	proxyRecords := h.proxy1.rec.snapshot()
	// 整条链路（A、302、B）都经同一代理；首个请求 407，之后全部带缓存代理凭据。
	if proxyRecords[0].ProxyAuthorization != "" {
		t.Fatalf("first proxy request must be bare")
	}
	for i := 1; i < len(proxyRecords); i++ {
		if proxyRecords[i].ProxyAuthorization != wantProxy {
			t.Fatalf("proxy request %d lost cached credential: %q", i, proxyRecords[i].ProxyAuthorization)
		}
	}

	// 两个源站都只看到自己的源站凭据，代理口令从未出现。
	wantA := "Basic " + basicToken("alice", "pass-a")
	wantB := "Basic " + basicToken("bob", "pass-b")
	for i, rec := range h.originA.rec.snapshot() {
		if rec.ProxyAuthorization != "" {
			t.Fatalf("origin A request %d saw proxy credential", i)
		}
		if rec.Authorization != "" && rec.Authorization != wantA {
			t.Fatalf("origin A request %d foreign origin credential: %q", i, rec.Authorization)
		}
	}
	for i, rec := range h.originB.rec.snapshot() {
		if rec.ProxyAuthorization != "" {
			t.Fatalf("origin B request %d saw proxy credential", i)
		}
		if rec.Authorization != "" && rec.Authorization != wantB {
			t.Fatalf("origin B request %d foreign origin credential: %q", i, rec.Authorization)
		}
		if rec.Authorization == wantA {
			t.Fatalf("origin B must never receive A's credential across redirect")
		}
	}
}

// 5. 切换代理必须重新挑战：代理 1 的缓存会话不能发给代理 2。
func TestSwitchingProxyForcesNewChallenge(t *testing.T) {
	h := newTestHarness(t)

	proxyCreds := NewStaticCredentials()
	proxyCreds.Add(originIDOf(t, h.proxy1Srv.URL), Credential{Username: "p1", Password: "pw1"})
	proxyCreds.Add(originIDOf(t, h.proxy2Srv.URL), Credential{Username: "p2", Password: "pw2"})
	// 源站 /public 不要求认证，本测试聚焦代理角色的分区。

	var currentProxy *url.URL
	tr := NewAuthTransport(AuthTransportConfig{
		Proxies: proxyCreds,
		Base:    newMemoryTransport(h.network),
		Proxy:   func(*http.Request) (*url.URL, error) { return currentProxy, nil },
	})
	client := tr.Client()

	// 先走代理 1：建立代理 1 的会话。
	currentProxy = proxyURLOf(t, h.proxy1Srv)
	if status, _ := getBody(t, client, h.originASrv.URL+"/public"); status != http.StatusOK {
		t.Fatalf("via proxy1 status = %d, want 200", status)
	}

	// 切到代理 2：对代理 2 而言必须是全新裸请求并触发它自己的 407。
	currentProxy = proxyURLOf(t, h.proxy2Srv)
	if status, _ := getBody(t, client, h.originASrv.URL+"/public"); status != http.StatusOK {
		t.Fatalf("via proxy2 status = %d, want 200", status)
	}

	p2Records := h.proxy2.rec.snapshot()
	if len(p2Records) == 0 {
		t.Fatalf("proxy2 received no requests")
	}
	if p2Records[0].ProxyAuthorization != "" {
		t.Fatalf("first request to new proxy must be bare, got %q", p2Records[0].ProxyAuthorization)
	}
	proxy1Cred := "Basic " + basicToken("p1", "pw1")
	for i, rec := range p2Records {
		if rec.ProxyAuthorization == proxy1Cred {
			t.Fatalf("proxy2 request %d received proxy1 credential: %q", i, rec.ProxyAuthorization)
		}
	}

	// 切回代理 1：会话仍按其身份缓存，应直接放行、不再 407。
	currentProxy = proxyURLOf(t, h.proxy1Srv)
	before := len(h.proxy1.rec.snapshot())
	if status, _ := getBody(t, client, h.originASrv.URL+"/public"); status != http.StatusOK {
		t.Fatalf("back to proxy1 status = %d, want 200", status)
	}
	after := h.proxy1.rec.snapshot()
	wantProxy1 := "Basic " + basicToken("p1", "pw1")
	if after[before].ProxyAuthorization != wantProxy1 {
		t.Fatalf("cached proxy1 session must be reused: %q", after[before].ProxyAuthorization)
	}
}

// 6. 连接池复用不能扩大范围：同一连接上的后续请求仍严格按身份取凭据，
// 更换目标身份即更换凭据集合，且不携带任何额外首部。
func TestConnectionReuseDoesNotExpandScope(t *testing.T) {
	h := newTestHarness(t)

	creds := NewStaticCredentials()
	creds.Add(originIDOf(t, h.originASrv.URL), Credential{Username: "alice", Password: "pass-a"})
	creds.Add(originIDOf(t, h.originBSrv.URL), Credential{Username: "bob", Password: "pass-b"})

	base := newMemoryTransport(h.network)
	base.MaxIdleConnsPerHost = 4
	tr := NewAuthTransport(AuthTransportConfig{Origins: creds, Base: base})
	client := tr.Client()

	// 对 A 连续三次请求，复用同一条空闲连接。
	for i := 0; i < 3; i++ {
		if status, _ := getBody(t, client, h.originASrv.URL+"/keep"); status != http.StatusOK {
			t.Fatalf("A request %d status = %d, want 200", i, status)
		}
	}
	wantA := "Basic " + basicToken("alice", "pass-a")
	recordsA := h.originA.rec.snapshot()
	// 首个调用是“裸请求 401 + 重试 200”，之后两次都应直接带缓存凭据；
	// 除首个裸请求外，任何到达 A 的请求都必须且只能携带 A 自己的凭据。
	if recordsA[0].Authorization != "" {
		t.Fatalf("first ever request must be bare, got %q", recordsA[0].Authorization)
	}
	for i := 1; i < len(recordsA); i++ {
		if recordsA[i].Authorization != wantA {
			t.Fatalf("A request %d credential = %q, want %q", i, recordsA[i].Authorization, wantA)
		}
	}

	// 再访问 B：连接池里虽有到 A 的热连接，也绝不能带上 A 的凭据。
	if status, _ := getBody(t, client, h.originBSrv.URL+"/other"); status != http.StatusOK {
		t.Fatalf("B status = %d, want 200", status)
	}
	bRecords := h.originB.rec.snapshot()
	lastB := bRecords[len(bRecords)-1]
	wantB := "Basic " + basicToken("bob", "pass-b")
	if lastB.Authorization != wantB {
		t.Fatalf("B credential = %q, want %q (scope must not expand via pool)", lastB.Authorization, wantB)
	}

	// 回到 A：仍只能用 A 的凭据，证明会话与身份绑定而非与连接绑定。
	if status, _ := getBody(t, client, h.originASrv.URL+"/keep-again"); status != http.StatusOK {
		t.Fatalf("A revisit status = %d, want 200", status)
	}
	aRecords := h.originA.rec.snapshot()
	if got := aRecords[len(aRecords)-1].Authorization; got != wantA {
		t.Fatalf("A revisit credential = %q, want %q", got, wantA)
	}
}

// 7. 口令错误必须有界重试：超过预算后把最后一次 401 交还，不形成死循环。
func TestWrongCredentialBoundedRetries(t *testing.T) {
	h := newTestHarness(t)

	creds := NewStaticCredentials()
	creds.Add(originIDOf(t, h.originASrv.URL), Credential{Username: "alice", Password: "WRONG"})

	tr := NewAuthTransport(AuthTransportConfig{Origins: creds, Base: newMemoryTransport(h.network)})
	status, _ := getBody(t, tr.Client(), h.originASrv.URL+"/secure")
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 after bounded retries", status)
	}

	records := h.originA.rec.snapshot()
	// 1 次裸请求 + maxAuthRetries 次带错误凭据的重试。
	wantAttempts := 1 + maxAuthRetries
	if len(records) != wantAttempts {
		t.Fatalf("origin attempts = %d, want exactly %d (no infinite loop)", len(records), wantAttempts)
	}
	if records[0].Authorization != "" {
		t.Fatalf("first attempt must be bare")
	}
	for i := 1; i < len(records); i++ {
		if !strings.HasPrefix(records[i].Authorization, "Basic ") {
			t.Fatalf("retry %d must carry Basic header", i)
		}
	}
}

// 8. 未配置凭据提供方时不发送任何首部，也不做重试，直接返回挑战。
func TestNoCredentialProviderPassesChallengeThrough(t *testing.T) {
	h := newTestHarness(t)

	tr := NewAuthTransport(AuthTransportConfig{Base: newMemoryTransport(h.network)})
	status, _ := getBody(t, tr.Client(), h.originASrv.URL+"/secure")
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 passed through", status)
	}
	records := h.originA.rec.snapshot()
	if len(records) != 1 {
		t.Fatalf("attempts = %d, want exactly 1 with no retries", len(records))
	}
	if records[0].Authorization != "" {
		t.Fatalf("no Authorization may be sent without a credential provider")
	}
}

// 9. Digest 完整握手 + stale=true：服务器轮换 nonce 后，
// 旧作用域作废、按新挑战重新应答，且重试仍在预算内。
func TestDigestStaleForcesFreshChallenge(t *testing.T) {
	digestOrigin := &originSpec{
		realm:     "realm-d",
		basicUser: "dave",
		basicPass: "pass-d",
		useDigest: true,
	}
	h := newTestHarness(t)
	srv := newOriginServer(t, h.network, "origin-digest.test:80", digestOrigin)

	creds := NewStaticCredentials()
	creds.Add(originIDOf(t, srv.URL), Credential{Username: "dave", Password: "pass-d"})
	tr := NewAuthTransport(AuthTransportConfig{Origins: creds, Base: newMemoryTransport(h.network)})

	// 第一次调用：裸请求 -> 401 Digest -> 计算 response -> 200。
	if status, _ := getBody(t, tr.Client(), srv.URL+"/digest"); status != http.StatusOK {
		t.Fatalf("digest handshake status = %d, want 200", status)
	}

	// 服务器轮换 nonce，使所有已缓存会话失效，下一次请求收到 stale=true。
	digestOrigin.digest.mu.Lock()
	oldNonce := digestOrigin.digest.nonce
	digestOrigin.digest.rotate()
	digestOrigin.digest.mu.Unlock()

	if status, _ := getBody(t, tr.Client(), srv.URL+"/digest2"); status != http.StatusOK {
		t.Fatalf("digest stale recovery status = %d, want 200", status)
	}

	records := digestOrigin.rec.snapshot()
	// 第二次调用应是：携带旧 nonce 的缓存请求(401 stale) -> 新 nonce 重试(200)。
	var sawStaleRetry bool
	for _, rec := range records {
		if strings.HasPrefix(rec.Authorization, "Digest ") {
			params := parseDigestHeader(rec.Authorization)
			if params["nonce"] != oldNonce {
				sawStaleRetry = true
			}
		}
	}
	if !sawStaleRetry {
		t.Fatalf("expected a retried Digest request carrying the fresh nonce")
	}

	// 新 nonce 会话建立后，再请求应一次成功、不再产生 401。
	before := len(records)
	if status, _ := getBody(t, tr.Client(), srv.URL+"/digest3"); status != http.StatusOK {
		t.Fatalf("digest cached session status = %d, want 200", status)
	}
	if got := len(digestOrigin.rec.snapshot()); got != before+1 {
		t.Fatalf("fresh session should be reused without another challenge: attempts delta=%d", got-before)
	}
}

// 10. 调用方手工塞入的认证首部必须被剥离并替换为受信决策结果，
// 防止重定向器/上层逻辑把口令带到错误身份。
func TestCallerSuppliedHeadersAreStripped(t *testing.T) {
	h := newTestHarness(t)

	creds := NewStaticCredentials()
	creds.Add(originIDOf(t, h.originASrv.URL), Credential{Username: "alice", Password: "pass-a"})
	tr := NewAuthTransport(AuthTransportConfig{Origins: creds, Base: newMemoryTransport(h.network)})

	req, err := http.NewRequest(http.MethodGet, h.originASrv.URL+"/resource", nil)
	if err != nil {
		t.Fatal(err)
	}
	// 调用方试图携带一个不属于本组件决策的伪造首部。
	req.Header.Set("Authorization", "Basic "+basicToken("mallory", "evil"))
	req.Header.Set("Proxy-Authorization", "Basic "+basicToken("rogue", "rogue"))

	resp, err := tr.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	records := h.originA.rec.snapshot()
	want := "Basic " + basicToken("alice", "pass-a")
	for i, rec := range records {
		if rec.Authorization == "Basic "+basicToken("mallory", "evil") {
			t.Fatalf("caller-supplied foreign credential leaked on request %d", i)
		}
		if rec.ProxyAuthorization != "" {
			t.Fatalf("Proxy-Authorization must never reach the origin, got %q", rec.ProxyAuthorization)
		}
	}
	if records[len(records)-1].Authorization != want {
		t.Fatalf("final request must carry the trusted credential, got %q", records[len(records)-1].Authorization)
	}
}

// 11. 并发使用同一传输层：保险库与 Digest 计数必须线程安全，
// 且每个请求仍只携带其目标身份的凭据。
func TestConcurrentRequestsKeepPartitions(t *testing.T) {
	h := newTestHarness(t)

	creds := NewStaticCredentials()
	creds.Add(originIDOf(t, h.originASrv.URL), Credential{Username: "alice", Password: "pass-a"})
	creds.Add(originIDOf(t, h.originBSrv.URL), Credential{Username: "bob", Password: "pass-b"})

	tr := NewAuthTransport(AuthTransportConfig{Origins: creds, Base: newMemoryTransport(h.network)})
	client := tr.Client()

	const goroutines = 12
	errCh := make(chan error, goroutines*2)
	var wg sync.WaitGroup
	hit := func(target, wantCred string) {
		defer wg.Done()
		resp, err := client.Get(target)
		if err != nil {
			errCh <- err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			errCh <- fmt.Errorf("status %d for %s", resp.StatusCode, target)
		}
	}
	for g := 0; g < goroutines; g++ {
		wg.Add(2)
		go hit(h.originASrv.URL+"/concurrent", "Basic "+basicToken("alice", "pass-a"))
		go hit(h.originBSrv.URL+"/concurrent", "Basic "+basicToken("bob", "pass-b"))
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	wantA := "Basic " + basicToken("alice", "pass-a")
	wantB := "Basic " + basicToken("bob", "pass-b")
	for i, rec := range h.originA.rec.snapshot() {
		if rec.Authorization != "" && rec.Authorization != wantA {
			t.Fatalf("A request %d foreign credential: %q", i, rec.Authorization)
		}
	}
	for i, rec := range h.originB.rec.snapshot() {
		if rec.Authorization != "" && rec.Authorization != wantB {
			t.Fatalf("B request %d foreign credential: %q", i, rec.Authorization)
		}
	}
	if len(h.originA.rec.snapshot()) < goroutines || len(h.originB.rec.snapshot()) < goroutines {
		t.Fatalf("expected concurrent traffic on both origins, got A=%d B=%d",
			len(h.originA.rec.snapshot()), len(h.originB.rec.snapshot()))
	}
}

// 12. realm 白名单越界：凭据只对受信 realm 应答，同主机上未受信的
// realm 挑战必须被拒绝，最终把 401 交还而不是试探发送口令。
func TestRealmBoundaryRejectsUnlistedRealm(t *testing.T) {
	h := newTestHarness(t)

	creds := NewStaticCredentials()
	creds.Add(originIDOf(t, h.originASrv.URL), Credential{
		Username:      "alice",
		Password:      "pass-a",
		TrustedRealms: []string{"some-other-realm"}, // 服务器实际给的是 realm-a
	})

	tr := NewAuthTransport(AuthTransportConfig{Origins: creds, Base: newMemoryTransport(h.network)})
	status, _ := getBody(t, tr.Client(), h.originASrv.URL+"/secure")
	if status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 when realm is not trusted", status)
	}
	for i, rec := range h.originA.rec.snapshot() {
		if rec.Authorization != "" {
			t.Fatalf("request %d must not answer an untrusted realm, got %q", i, rec.Authorization)
		}
	}
}
