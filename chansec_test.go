package netsec

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	metaIPPort = "10.0.0.1:7600"
	metaHost   = "metadata.internal:7600"
	bizIPPort  = "10.0.0.2:80"
	bizHost    = "biz.example:80"
	cdnIPPort  = "10.0.0.3:80"
	cdnHost    = "cdn.example:80"
)

func newLauncher(t *testing.T) *Launcher {
	t.Helper()
	l, err := NewLauncher()
	if err != nil {
		t.Fatalf("NewLauncher: %v", err)
	}
	return l
}

// testEnv 搭建伪网络拓扑：元数据端点 + 业务端点，
// 另注册一个解析到元数据地址的“邪恶”业务域名。
type testEnv struct {
	net      *pseudoNet
	launcher *Launcher
	channel  *Channel
}

func newTestEnv(t *testing.T, metaHandler http.Handler) *testEnv {
	t.Helper()
	pn := newPseudoNet()
	pn.addEndpoint(t, metaIPPort, []string{metaHost, "evil.example:80"}, metaHandler)
	launcher := newLauncher(t)
	channel := NewChannel(Config{
		EndpointHost:      metaHost,
		ProtectedAddrs:    []string{metaIPPort},
		CredentialHeaders: []string{"Authorization", "Proxy-Authorization", "Cookie", "X-Api-Key"},
		BaseDial:          pn.dial,
	}, launcher.Verifier())
	return &testEnv{net: pn, launcher: launcher, channel: channel}
}

// TestPathNormalization 验证路径规范化：穿越序列、重复分隔符、编码点段
// 在规范化后收敛，前缀匹配按路径段边界执行。
func TestPathNormalization(t *testing.T) {
	launcher := newLauncher(t)
	verifier := launcher.Verifier()

	cap, err := launcher.Mint("/meta/", time.Minute)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if cap.Prefix != "/meta" {
		t.Fatalf("prefix not normalized, got %q", cap.Prefix)
	}

	cases := []struct {
		name    string
		path    string
		wantErr error
	}{
		{"exact prefix", "/meta", nil},
		{"child", "/meta/data", nil},
		{"double slash", "//meta//data", nil},
		{"dot segments stay inside", "/meta/../meta/data", nil},
		{"dot segment escapes", "/meta/../etc/secret", ErrPathOutsidePrefix},
		{"encoded traversal escapes", "/meta/%2e%2e/etc/secret", ErrPathOutsidePrefix},
		{"sibling not matched", "/metadata", ErrPathOutsidePrefix},
		{"parent not matched", "/", ErrPathOutsidePrefix},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 模拟请求路径经 URL 解码后的形态。
			decoded, err := url.PathUnescape(tc.path)
			if err != nil {
				t.Fatalf("unescape: %v", err)
			}
			err = verifier.Verify(cap.Token(), decoded)
			if tc.wantErr == nil && err != nil {
				t.Fatalf("Verify(%q) = %v, want nil", tc.path, err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("Verify(%q) = %v, want %v", tc.path, err, tc.wantErr)
			}
		})
	}

	if _, err := launcher.Mint("meta/data", time.Minute); !errors.Is(err, ErrBadPrefix) {
		t.Fatalf("Mint(relative) = %v, want ErrBadPrefix", err)
	}
}

// TestTokenIntegrity 验证伪造、篡改与过期的令牌被拒绝。
func TestTokenIntegrity(t *testing.T) {
	launcher := newLauncher(t)
	verifier := launcher.Verifier()

	cap, err := launcher.Mint("/meta", time.Minute)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if err := verifier.Verify(cap.Token(), "/meta/x"); err != nil {
		t.Fatalf("Verify valid token: %v", err)
	}

	// 篡改载荷（前缀换成根路径）但沿用原签名。
	parts := strings.Split(cap.Token(), ".")
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	payload["p"] = "/"
	tampered, _ := json.Marshal(payload)
	forged := base64.RawURLEncoding.EncodeToString(tampered) + "." + parts[1]
	if err := verifier.Verify(forged, "/anything"); !errors.Is(err, ErrCapabilityForged) {
		t.Fatalf("Verify(tampered) = %v, want ErrCapabilityForged", err)
	}

	if err := verifier.Verify("not-a-token", "/meta"); !errors.Is(err, ErrCapabilityMalformed) {
		t.Fatalf("Verify(garbage) = %v, want ErrCapabilityMalformed", err)
	}

	expired, err := launcher.Mint("/meta", -time.Second)
	if err != nil {
		t.Fatalf("Mint expired: %v", err)
	}
	if err := verifier.Verify(expired.Token(), "/meta"); !errors.Is(err, ErrCapabilityExpired) {
		t.Fatalf("Verify(expired) = %v, want ErrCapabilityExpired", err)
	}
}

// headerRecorder 记录到达伪端点的请求头。
type headerRecorder struct {
	mu     sync.Mutex
	header http.Header
}

func (r *headerRecorder) record(h http.Header) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.header = h.Clone()
}

func (r *headerRecorder) get(key string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.header.Get(key)
}

// TestCredentialHeaderCrossHop 验证跨源重定向剥离凭据头，同源跳转保留。
func TestCredentialHeaderCrossHop(t *testing.T) {
	var landing headerRecorder
	var sameOriginLanding headerRecorder

	env := newTestEnv(t, http.NotFoundHandler())
	env.net.addEndpoint(t, cdnIPPort, []string{cdnHost}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		landing.record(r.Header)
		fmt.Fprintln(w, "landed")
	}))
	env.net.addEndpoint(t, bizIPPort, []string{bizHost}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cross":
			http.Redirect(w, r, "http://"+cdnHost+"/landing", http.StatusFound)
		case "/local":
			http.Redirect(w, r, "/landing2", http.StatusFound)
		case "/landing2":
			sameOriginLanding.record(r.Header)
			fmt.Fprintln(w, "landed local")
		}
	}))

	client := env.channel.Client(nil)

	// 跨源跳转：凭据头不得到达第二跳。
	req, _ := http.NewRequest(http.MethodGet, "http://"+bizHost+"/cross", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	req.Header.Set("X-Api-Key", "api-key-value")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("cross-origin redirect: %v", err)
	}
	resp.Body.Close()
	if got := landing.get("Authorization"); got != "" {
		t.Fatalf("Authorization leaked cross-hop: %q", got)
	}
	if got := landing.get("X-Api-Key"); got != "" {
		t.Fatalf("X-Api-Key leaked cross-hop: %q", got)
	}

	// 同源跳转：凭据头保留。
	req, _ = http.NewRequest(http.MethodGet, "http://"+bizHost+"/local", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	req.Header.Set("X-Api-Key", "api-key-value")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("same-origin redirect: %v", err)
	}
	resp.Body.Close()
	if got := sameOriginLanding.get("Authorization"); got != "Bearer secret-token" {
		t.Fatalf("same-origin Authorization = %q, want preserved", got)
	}
	if got := sameOriginLanding.get("X-Api-Key"); got != "api-key-value" {
		t.Fatalf("same-origin X-Api-Key = %q, want preserved", got)
	}
}

// TestRedirectIntoMetadataChannel 验证重定向进入元数据通道必须重新取得能力。
func TestRedirectIntoMetadataChannel(t *testing.T) {
	var mu sync.Mutex
	metaHits := 0
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		metaHits++
		mu.Unlock()
		fmt.Fprintln(w, "metadata")
	}))
	env.net.addEndpoint(t, bizIPPort, []string{bizHost}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+metaHost+"/meta/data", http.StatusFound)
	}))

	countHits := func() int {
		mu.Lock()
		defer mu.Unlock()
		return metaHits
	}

	// 1. 无 approver：重定向被阻断，元数据端点零命中。
	client := env.channel.Client(nil)
	if _, err := client.Get("http://" + bizHost + "/go"); !errors.Is(err, ErrRedirectNeedsCapability) {
		t.Fatalf("redirect without approver = %v, want ErrRedirectNeedsCapability", err)
	}
	if n := countHits(); n != 0 {
		t.Fatalf("metadata endpoint hit %d times without capability", n)
	}

	// 2. approver 返回错误前缀的能力：仍被阻断。
	client = env.channel.Client(func(ctx context.Context, target *url.URL, via []*http.Request) (*Capability, error) {
		return env.launcher.Mint("/other/", time.Minute)
	})
	if _, err := client.Get("http://" + bizHost + "/go"); !errors.Is(err, ErrPathOutsidePrefix) {
		t.Fatalf("redirect with wrong-prefix capability = %v, want ErrPathOutsidePrefix", err)
	}
	if n := countHits(); n != 0 {
		t.Fatalf("metadata endpoint hit %d times with wrong-prefix capability", n)
	}

	// 3. approver 重新取得匹配能力：放行且恰好命中一次。
	client = env.channel.Client(func(ctx context.Context, target *url.URL, via []*http.Request) (*Capability, error) {
		return env.launcher.Mint("/meta/", time.Minute)
	})
	resp, err := client.Get("http://" + bizHost + "/go")
	if err != nil {
		t.Fatalf("redirect with fresh capability: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "metadata") {
		t.Fatalf("unexpected body %q", body)
	}
	if n := countHits(); n != 1 {
		t.Fatalf("metadata endpoint hit %d times, want 1", n)
	}
}

// TestCapabilityAndDialAddress 验证每次请求同时校验能力与实际拨号地址。
func TestCapabilityAndDialAddress(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "metadata")
	}))
	client := env.channel.Client(nil)

	// 1. 无能力直连元数据端点：拒绝。
	if _, err := client.Get("http://" + metaHost + "/meta/data"); !errors.Is(err, ErrCapabilityRequired) {
		t.Fatalf("direct request without capability = %v, want ErrCapabilityRequired", err)
	}

	// 2. 持有效能力：放行，且拨号记录命中受保护地址。
	cap, err := env.launcher.Mint("/meta/", time.Minute)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://"+metaHost+"/meta/data", nil)
	resp, err := client.Do(req.WithContext(WithCapability(req.Context(), cap)))
	if err != nil {
		t.Fatalf("request with capability: %v", err)
	}
	resp.Body.Close()
	dialed := env.channel.Dialed()
	if len(dialed) == 0 || dialed[len(dialed)-1] != metaIPPort {
		t.Fatalf("Dialed() = %v, want last entry %q", dialed, metaIPPort)
	}

	// 3. 能力前缀不覆盖请求路径：拒绝。
	narrow, err := env.launcher.Mint("/meta/other/", time.Minute)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	req, _ = http.NewRequest(http.MethodGet, "http://"+metaHost+"/meta/data", nil)
	if _, err := client.Do(req.WithContext(WithCapability(req.Context(), narrow))); !errors.Is(err, ErrPathOutsidePrefix) {
		t.Fatalf("request outside prefix = %v, want ErrPathOutsidePrefix", err)
	}

	// 4. 业务域名被解析到元数据地址（绕过 URL 主机比对）：拨号守卫拦截。
	if _, err := client.Get("http://evil.example:80/meta/data"); !errors.Is(err, ErrDialProtected) {
		t.Fatalf("rebound dial without capability = %v, want ErrDialProtected", err)
	}
}

// TestGenerationReuse 验证进程代次轮换后旧能力失效、新能力可用。
func TestGenerationReuse(t *testing.T) {
	env := newTestEnv(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "metadata")
	}))

	stale, err := env.launcher.Mint("/meta/", time.Minute)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if err := env.launcher.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	// 旧代次能力：校验层拒绝。
	if err := env.launcher.Verifier().Verify(stale.Token(), "/meta/data"); !errors.Is(err, ErrCapabilityStale) {
		t.Fatalf("Verify(stale) = %v, want ErrCapabilityStale", err)
	}

	// 旧代次能力：请求层同样拒绝。
	client := env.channel.Client(nil)
	req, _ := http.NewRequest(http.MethodGet, "http://"+metaHost+"/meta/data", nil)
	if _, err := client.Do(req.WithContext(WithCapability(req.Context(), stale))); !errors.Is(err, ErrCapabilityStale) {
		t.Fatalf("request with stale capability = %v, want ErrCapabilityStale", err)
	}

	// 新代次能力：正常放行。
	fresh, err := env.launcher.Mint("/meta/", time.Minute)
	if err != nil {
		t.Fatalf("Mint after restart: %v", err)
	}
	req, _ = http.NewRequest(http.MethodGet, "http://"+metaHost+"/meta/data", nil)
	resp, err := client.Do(req.WithContext(WithCapability(req.Context(), fresh)))
	if err != nil {
		t.Fatalf("request with fresh capability after restart: %v", err)
	}
	resp.Body.Close()
}
