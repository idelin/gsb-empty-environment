package netsec

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// 发送阶段的哨兵错误。
var (
	// ErrTooManyRedirects 表示重定向次数超过上限。
	ErrTooManyRedirects = errors.New("netsec: too many redirects")
	// ErrPushRejected 表示推送服务返回了非成功状态码。
	ErrPushRejected = errors.New("netsec: push service rejected the request")
	// ErrMissingEncryptionKeys 表示携带载荷但订阅缺少 RFC 8291 密钥材料。
	ErrMissingEncryptionKeys = errors.New("netsec: payload requires p256dh and auth keys")
	// ErrSubscriptionGone 表示推送服务以 404/410 告知订阅已失效。
	ErrSubscriptionGone = errors.New("netsec: subscription is no longer valid")
)

// Urgency 为 RFC 8030 的推送紧急度。
type Urgency string

const (
	UrgencyVeryLow Urgency = "very-low"
	UrgencyLow     Urgency = "low"
	UrgencyNormal  Urgency = "normal"
	UrgencyHigh    Urgency = "high"
)

func (u Urgency) valid() bool {
	switch u {
	case UrgencyVeryLow, UrgencyLow, UrgencyNormal, UrgencyHigh:
		return true
	default:
		return false
	}
}

// SendInput 为一次推送的入参。Subscription 与 SubscriptionID 二选一：
// 当 Sender 持有 Registry 时可只传 SubscriptionID，由发送器完成租户校验。
type SendInput struct {
	TenantID       string
	Subscription   *Subscription
	SubscriptionID string
	Topic          string
	Payload        []byte
	TTL            time.Duration
	Urgency        Urgency
}

// SendResult 为一次推送的结果。
type SendResult struct {
	StatusCode int
	MessageID  string
	Attempts   int
	// Replaced 为同订阅同 Topic 下被替换的旧待投递通知，首次写入时为 nil。
	Replaced *PendingNotification
}

// Sender 在 Guard 保护下执行 Web Push 投递，
// 手动逐跳跟随重定向，每一跳都重新执行端点安全校验。
type Sender struct {
	Guard        *Guard
	VAPID        *VAPIDCredentials
	Registry     *Registry
	Topics       *TopicStore
	HTTPClient   *http.Client
	MaxRedirects int
}

func (s *Sender) initDefaults() {
	if s.MaxRedirects <= 0 {
		s.MaxRedirects = 3
	}
}

// Send 投递一条通知。重定向到内网、非 https 或非白名单主机时会被直接拒绝。
func (s *Sender) Send(ctx context.Context, input SendInput) (*SendResult, error) {
	if s.Guard == nil || s.VAPID == nil || s.VAPID.PrivateKey == nil {
		return nil, errors.New("netsec: Sender requires Guard and VAPID credentials")
	}
	s.initDefaults()

	sub, err := s.resolveSubscription(input)
	if err != nil {
		return nil, err
	}
	if input.Topic != "" {
		if err := ValidateTopic(input.Topic); err != nil {
			return nil, err
		}
	}

	body, err := s.buildBody(sub, input)
	if err != nil {
		return nil, err
	}

	current := cloneURL(sub.Endpoint)
	client := s.httpClient()
	var response *http.Response
	attempts := 0

	// 手动重定向循环：每一跳都重新过白名单与解析 IP 复核（最多 MaxRedirects 次跳转），
	// 且 VAPID 头按新目标源重新签发，避免向不可信源泄漏授权令牌。
	for {
		if _, err := s.Guard.ValidateEndpoint(ctx, current.String()); err != nil {
			return nil, fmt.Errorf("netsec: redirect target rejected: %w", err)
		}
		request, err := s.newRequest(ctx, current, sub, input, body)
		if err != nil {
			return nil, err
		}
		response, err = client.Do(request)
		if err != nil {
			return nil, fmt.Errorf("netsec: deliver push: %w", err)
		}
		attempts++

		if response.StatusCode < 300 || response.StatusCode >= 400 {
			break
		}
		if err := response.Body.Close(); err != nil {
			return nil, err
		}
		location := response.Header.Get("Location")
		if location == "" {
			return nil, fmt.Errorf("%w: status %d without Location header", ErrPushRejected, response.StatusCode)
		}
		if attempts >= s.MaxRedirects {
			return nil, ErrTooManyRedirects
		}
		next, err := url.Parse(location)
		if err != nil {
			return nil, fmt.Errorf("netsec: parse redirect location: %w", err)
		}
		current = current.ResolveReference(next)
	}
	defer response.Body.Close()

	result := &SendResult{
		StatusCode: response.StatusCode,
		MessageID:  response.Header.Get("Location"),
		Attempts:   attempts,
	}
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusGone {
		return result, fmt.Errorf("%w: status %d", ErrSubscriptionGone, response.StatusCode)
	}
	switch response.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusAccepted:
	default:
		return result, fmt.Errorf("%w: status %d", ErrPushRejected, response.StatusCode)
	}

	if result.MessageID == "" {
		result.MessageID = newMessageID()
	}
	if input.Topic != "" && s.Topics != nil {
		replaced, replaceErr := s.Topics.Replace(
			input.TenantID, sub.ID, input.Topic, input.Payload, result.MessageID, s.Registry,
		)
		if replaceErr != nil {
			return nil, replaceErr
		}
		result.Replaced = replaced
	}
	return result, nil
}

func (s *Sender) resolveSubscription(input SendInput) (*Subscription, error) {
	switch {
	case input.Subscription != nil:
		if input.TenantID != "" && input.Subscription.TenantID != input.TenantID {
			return nil, ErrTenantMismatch
		}
		return input.Subscription, nil
	case s.Registry != nil && input.SubscriptionID != "":
		return s.Registry.Get(input.TenantID, input.SubscriptionID)
	default:
		return nil, errors.New("netsec: SendInput requires Subscription or a Registry-backed SubscriptionID")
	}
}

func (s *Sender) buildBody(sub *Subscription, input SendInput) ([]byte, error) {
	if len(input.Payload) == 0 {
		return nil, nil
	}
	if sub == nil || len(sub.Keys.P256DH) == 0 || len(sub.Keys.Auth) == 0 {
		return nil, ErrMissingEncryptionKeys
	}
	return EncryptPayload(sub.Keys, input.Payload)
}

func (s *Sender) newRequest(ctx context.Context, endpoint *url.URL, sub *Subscription, input SendInput, body []byte) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	// 每次跳转后的目标源可能不同，audience 按当前请求源重新生成。
	audience := endpoint.Scheme + "://" + endpoint.Host
	authorization, err := s.VAPID.Authorization(audience)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", authorization)

	ttl := input.TTL
	if ttl <= 0 {
		ttl = 28 * 24 * time.Hour
	}
	request.Header.Set("TTL", fmt.Sprintf("%d", int64(ttl.Seconds())))

	if input.Urgency != "" {
		if !input.Urgency.valid() {
			return nil, fmt.Errorf("netsec: invalid urgency %q", input.Urgency)
		}
		request.Header.Set("Urgency", string(input.Urgency))
	}
	if input.Topic != "" {
		request.Header.Set("Topic", input.Topic)
	}
	if len(body) > 0 {
		request.Header.Set("Content-Encoding", "aes128gcm")
		request.Header.Set("Content-Type", "application/octet-stream")
	}
	return request, nil
}

func (s *Sender) httpClient() *http.Client {
	if s.HTTPClient != nil {
		return s.HTTPClient
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = s.Guard.DialContext
	return &http.Client{
		Transport: transport,
		// 关闭自动跳转，所有 3xx 由 Send 的安全循环处理。
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func newMessageID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return time.Now().Format("20060102T150405.000000000")
	}
	return hex.EncodeToString(raw[:])
}
