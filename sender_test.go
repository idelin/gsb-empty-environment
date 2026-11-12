package netsec

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

const (
	publicHost   = "push.example.com"
	internalHost = "intranet.internal.example.com"
	externalHost = "push.third-party.example.net"
	testPort     = 8443
)

type senderTestHarness struct {
	guard    *Guard
	registry *Registry
	topics   *TopicStore
	sender   *Sender
	inner    *fakeRoundTripper
	endpoint string
}

func newSenderHarness(t *testing.T, handler func(*http.Request) (*http.Response, error)) *senderTestHarness {
	t.Helper()
	resolver := fakeResolver{hosts: map[string][]net.IPAddr{
		publicHost:   {mustIPAddr(t, "127.0.0.1")},
		externalHost: {mustIPAddr(t, "127.0.0.2")},
		internalHost: {mustIPAddr(t, "10.0.0.8")},
	}}
	guard := testGuard(t, resolver, publicHost, externalHost, internalHost)
	guard.AllowedPorts = []int{443, testPort}

	inner := &fakeRoundTripper{handler: handler}
	registry := NewRegistry(guard)
	topics := NewTopicStore()
	vapid, err := NewVAPIDCredentials("mailto:push@example.com")
	if err != nil {
		t.Fatalf("vapid credentials: %v", err)
	}
	sender := &Sender{
		Guard:    guard,
		VAPID:    vapid,
		Registry: registry,
		Topics:   topics,
		HTTPClient: &http.Client{
			Transport: &fakeTransportWithDialer{guard: guard, inner: inner},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		MaxRedirects: 3,
	}
	return &senderTestHarness{
		guard:    guard,
		registry: registry,
		topics:   topics,
		sender:   sender,
		inner:    inner,
		endpoint: "https://" + publicHost + ":" + strconv.Itoa(testPort) + "/push/sub-1",
	}
}

func (h *senderTestHarness) createSubscription(t *testing.T, tenant string, withKeys bool) *Subscription {
	t.Helper()
	var keys SubscriptionKeys
	if withKeys {
		keys = generateSubscriptionKeys(t)
	}
	sub, err := h.registry.Create(context.Background(), CreateSubscriptionInput{
		TenantID:         tenant,
		RawEndpoint:      h.endpoint,
		VAPIDFingerprint: fingerprintForTest(t),
		Keys:             keys,
	})
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}
	return sub
}

func (h *senderTestHarness) redirectURL(host, target string) string {
	return "https://" + host + ":" + strconv.Itoa(testPort) + target
}

// 场景一：推送服务把请求重定向到解析为内网地址的主机，必须被拦截。
func TestSendRejectsIntranetRedirect(t *testing.T) {
	var harness *senderTestHarness
	harness = newSenderHarness(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/push/sub-1" {
			return redirectResponse(harness.redirectURL(internalHost, "/internal-push"), http.StatusFound), nil
		}
		return textResponse(http.StatusAccepted, nil, ""), nil
	})
	sub := harness.createSubscription(t, "tenant-a", false)

	_, err := harness.sender.Send(context.Background(), SendInput{
		TenantID:     "tenant-a",
		Subscription: sub,
	})
	assertErrorIs(t, err, ErrReservedIP)

	requests := harness.inner.snapshot()
	if len(requests) != 1 || requests[0].URL.Path != "/push/sub-1" {
		t.Fatalf("only the first request should be dispatched, got %+v", requests)
	}
}

// 场景二：重定向到非白名单的外部主机，同样拒绝。
func TestSendRejectsNonWhitelistedRedirect(t *testing.T) {
	var harness *senderTestHarness
	harness = newSenderHarness(t, func(r *http.Request) (*http.Response, error) {
		return redirectResponse(harness.redirectURL("evil.example.org", "/elsewhere"), http.StatusFound), nil
	})
	sub := harness.createSubscription(t, "tenant-a", false)

	_, err := harness.sender.Send(context.Background(), SendInput{
		TenantID:     "tenant-a",
		Subscription: sub,
	})
	assertErrorIs(t, err, ErrNotWhitelisted)
}

// 场景三：重定向把 https 降级为 http，按非 https 端点拒绝。
func TestSendRejectsHTTPDowngradeRedirect(t *testing.T) {
	var harness *senderTestHarness
	harness = newSenderHarness(t, func(r *http.Request) (*http.Response, error) {
		return redirectResponse("http://"+publicHost+":"+strconv.Itoa(testPort)+"/downgraded", http.StatusFound), nil
	})
	sub := harness.createSubscription(t, "tenant-a", false)

	_, err := harness.sender.Send(context.Background(), SendInput{
		TenantID:     "tenant-a",
		Subscription: sub,
	})
	assertErrorIs(t, err, ErrNonHTTPS)
}

// 正向场景：重定向仍在白名单内时逐跳重签 VAPID 并成功投递。
func TestSendFollowsSafeRedirectAndSignsEachHop(t *testing.T) {
	var harness *senderTestHarness
	harness = newSenderHarness(t, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/push/sub-1":
			return redirectResponse(harness.redirectURL(externalHost, "/relay"), http.StatusFound), nil
		case "/relay":
			return textResponse(http.StatusCreated, map[string]string{
				"Location": "https://push.example.com/messages/msg-42",
			}, ""), nil
		default:
			return textResponse(http.StatusInternalServerError, nil, ""), nil
		}
	})
	sub := harness.createSubscription(t, "tenant-a", false)

	result, err := harness.sender.Send(context.Background(), SendInput{
		TenantID:     "tenant-a",
		Subscription: sub,
		Topic:        "daily-digest",
		Urgency:      UrgencyHigh,
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if result.Attempts != 2 || result.StatusCode != http.StatusCreated || result.MessageID != "https://push.example.com/messages/msg-42" {
		t.Fatalf("unexpected result: %+v", result)
	}

	requests := harness.inner.snapshot()
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}
	for index, request := range requests {
		auth := request.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "vapid-t=") {
			t.Fatalf("hop %d missing VAPID header: %q", index, auth)
		}
		if request.Header.Get("Topic") != "daily-digest" {
			t.Fatalf("hop %d missing Topic header", index)
		}
		if request.Header.Get("Urgency") != string(UrgencyHigh) || request.Header.Get("TTL") == "" {
			t.Fatalf("hop %d missing Urgency/TTL headers", index)
		}
	}
	if requests[0].URL.Hostname() != publicHost || requests[1].URL.Hostname() != externalHost {
		t.Fatalf("redirect hosts = %q -> %q", requests[0].URL.Hostname(), requests[1].URL.Hostname())
	}
}

func TestSendEncryptsPayloadAndDelivers(t *testing.T) {
	keys := generateSubscriptionKeys(t)
	harness := newSenderHarness(t, func(r *http.Request) (*http.Response, error) {
		return textResponse(http.StatusAccepted, map[string]string{
			"Location": "https://push.example.com/messages/1",
		}, ""), nil
	})
	sub := harness.createSubscription(t, "tenant-a", false)
	sub.Keys = keys

	result, err := harness.sender.Send(context.Background(), SendInput{
		TenantID:     "tenant-a",
		Subscription: sub,
		Payload:      []byte("secret notification"),
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if result.Replaced != nil {
		t.Fatal("topic-less message should have no replacement")
	}

	requests := harness.inner.snapshot()
	if len(requests) != 1 {
		t.Fatalf("requests = %d", len(requests))
	}
	request := requests[0]
	if request.Header.Get("Content-Encoding") != "aes128gcm" {
		t.Fatalf("Content-Encoding = %q", request.Header.Get("Content-Encoding"))
	}
	body, _ := io.ReadAll(request.Body)
	if strings.Contains(string(body), "secret notification") {
		t.Fatal("plaintext payload leaked into request body")
	}
	if len(body) <= saltLen+4+1+uncompressedPointLen {
		t.Fatal("encrypted body missing")
	}
}

func TestSendRejectsPayloadWithoutKeys(t *testing.T) {
	harness := newSenderHarness(t, func(r *http.Request) (*http.Response, error) {
		return textResponse(http.StatusAccepted, nil, ""), nil
	})
	sub := harness.createSubscription(t, "tenant-a", false)

	_, err := harness.sender.Send(context.Background(), SendInput{
		TenantID:     "tenant-a",
		Subscription: sub,
		Payload:      []byte("no keys"),
	})
	assertErrorIs(t, err, ErrMissingEncryptionKeys)
}

func TestSendRejectsOverlongTopic(t *testing.T) {
	harness := newSenderHarness(t, func(r *http.Request) (*http.Response, error) {
		return textResponse(http.StatusAccepted, nil, ""), nil
	})
	sub := harness.createSubscription(t, "tenant-a", false)

	_, err := harness.sender.Send(context.Background(), SendInput{
		TenantID:     "tenant-a",
		Subscription: sub,
		Topic:        strings.Repeat("x", MaxTopicLen+1),
	})
	assertErrorIs(t, err, ErrInvalidTopic)
}

func TestSendRejectsPushServiceErrorsAndGone(t *testing.T) {
	harness := newSenderHarness(t, func(r *http.Request) (*http.Response, error) {
		return textResponse(http.StatusGone, nil, ""), nil
	})
	sub := harness.createSubscription(t, "tenant-a", false)

	_, err := harness.sender.Send(context.Background(), SendInput{
		TenantID:     "tenant-a",
		Subscription: sub,
	})
	assertErrorIs(t, err, ErrSubscriptionGone)

	// 订阅创建阶段就拒绝内网端点，攻击者无法注册内网订阅。
	_, err = harness.registry.Create(context.Background(), CreateSubscriptionInput{
		TenantID:         "tenant-a",
		RawEndpoint:      "https://" + internalHost + "/steal",
		VAPIDFingerprint: fingerprintForTest(t),
	})
	if !errors.Is(err, ErrReservedIP) {
		t.Fatalf("intranet subscription: error = %v, want ErrReservedIP", err)
	}
}

func TestSendViaRegistryIDEnforcesTenant(t *testing.T) {
	harness := newSenderHarness(t, func(r *http.Request) (*http.Response, error) {
		return textResponse(http.StatusAccepted, nil, ""), nil
	})
	sub := harness.createSubscription(t, "tenant-a", false)

	// 凭其他租户身份发送，按未知订阅拒绝。
	_, err := harness.sender.Send(context.Background(), SendInput{
		TenantID:       "tenant-b",
		SubscriptionID: sub.ID,
	})
	assertErrorIs(t, err, ErrUnknownSubscription)

	// 正确租户可直接凭 ID 发送，且同 Topic 的第二次投递返回被替换条目。
	first, err := harness.sender.Send(context.Background(), SendInput{
		TenantID:       "tenant-a",
		SubscriptionID: sub.ID,
		Topic:          "balance",
	})
	if err != nil {
		t.Fatalf("first send: %v", err)
	}
	second, err := harness.sender.Send(context.Background(), SendInput{
		TenantID:       "tenant-a",
		SubscriptionID: sub.ID,
		Topic:          "balance",
	})
	if err != nil {
		t.Fatalf("second send: %v", err)
	}
	if first.Replaced != nil || second.Replaced == nil {
		t.Fatalf("replacement semantics wrong: first=%+v second=%+v", first.Replaced, second.Replaced)
	}
}

func TestSendRedirectLoopLimit(t *testing.T) {
	var harness *senderTestHarness
	harness = newSenderHarness(t, func(r *http.Request) (*http.Response, error) {
		return redirectResponse(harness.redirectURL(publicHost, r.URL.Path+"/next"), http.StatusFound), nil
	})
	harness.sender.MaxRedirects = 2
	sub := harness.createSubscription(t, "tenant-a", false)

	_, err := harness.sender.Send(context.Background(), SendInput{
		TenantID:     "tenant-a",
		Subscription: sub,
	})
	assertErrorIs(t, err, ErrTooManyRedirects)
}

func TestSendRejectsRedirectWithoutLocation(t *testing.T) {
	harness := newSenderHarness(t, func(r *http.Request) (*http.Response, error) {
		return textResponse(http.StatusMovedPermanently, nil, ""), nil
	})
	sub := harness.createSubscription(t, "tenant-a", false)

	_, err := harness.sender.Send(context.Background(), SendInput{
		TenantID:     "tenant-a",
		Subscription: sub,
	})
	assertErrorIs(t, err, ErrPushRejected)
}
