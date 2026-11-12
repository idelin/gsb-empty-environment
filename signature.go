package netsec

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
)

const (
	// SignatureHeader is the WebSub content signature header.
	SignatureHeader = "X-Hub-Signature"
	// SignaturePrefix identifies the HMAC-SHA256 signature algorithm.
	SignaturePrefix = "sha256="
	// XHubSignatureHeader is an alias for SignatureHeader.
	XHubSignatureHeader = SignatureHeader
)

// Signature computes the X-Hub-Signature value for a payload.
func Signature(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(payload)
	return SignaturePrefix + hex.EncodeToString(mac.Sum(nil))
}

// SignPayload is an alias for Signature.
func SignPayload(secret string, payload []byte) string {
	return Signature(secret, payload)
}

// Sign is an alias for Signature.
func Sign(secret string, payload []byte) string {
	return Signature(secret, payload)
}

// SignBytes is an alias for Signature with the payload first.
func SignBytes(payload []byte, secret string) string {
	return Signature(secret, payload)
}

// Signature computes the X-Hub-Signature value for a payload.
func (m *Manager) Signature(secret string, payload []byte) string {
	return Signature(secret, payload)
}

// Sign is an alias for Manager.Signature.
func (m *Manager) Sign(secret string, payload []byte) string {
	return Signature(secret, payload)
}

// SetSignature sets X-Hub-Signature on headers when a secret is configured.
func SetSignature(headers http.Header, secret string, payload []byte) {
	if secret == "" {
		return
	}
	headers.Set(SignatureHeader, Signature(secret, payload))
}

// SignForSubscription returns the signature for a stored subscription.
func (m *Manager) SignForSubscription(callback, topic string, payload []byte) (string, bool) {
	sub, ok := m.Subscription(callback, topic)
	if !ok || sub.Secret == "" {
		return "", false
	}
	return Signature(sub.Secret, payload), true
}

// Distribute posts content to every active subscriber of topic. A
// subscription with a non-empty secret receives an X-Hub-Signature header
// containing sha256= followed by the HMAC-SHA256 hexadecimal digest.
func (m *Manager) Distribute(ctx context.Context, topic string, payload []byte) []DeliveryResult {
	normalizedTopic, err := validateTopic(topic)
	if err != nil {
		return []DeliveryResult{{Topic: topic, Err: err}}
	}

	subs := make([]Subscription, 0)
	for _, sub := range m.Subscriptions() {
		if sub.Topic == normalizedTopic {
			subs = append(subs, sub)
		}
	}
	if len(subs) == 0 {
		return nil
	}

	results := make([]DeliveryResult, 0, len(subs))
	for _, sub := range subs {
		results = append(results, m.deliver(ctx, sub, payload))
	}
	return results
}

func (m *Manager) deliver(ctx context.Context, sub Subscription, payload []byte) DeliveryResult {
	result := DeliveryResult{
		Callback: sub.Callback,
		Topic:    sub.Topic,
	}

	callbackURL, err := parseCallbackURL(sub.Callback)
	if err != nil {
		result.Err = err
		return result
	}
	if err := m.validateCallbackURL(ctx, callbackURL); err != nil {
		result.Err = err
		return result
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, callbackURL.String(), bytes.NewReader(payload))
	if err != nil {
		result.Err = fmt.Errorf("websub: create distribution request: %w", err)
		return result
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("User-Agent", "netsec-websub/1.0")
	if sub.Secret != "" {
		req.Header.Set(SignatureHeader, Signature(sub.Secret, payload))
	}

	resp, err := m.client.Do(req)
	if err != nil {
		result.Err = err
		return result
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	result.StatusCode = resp.StatusCode
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.Err = fmt.Errorf("websub: callback returned HTTP %d", resp.StatusCode)
	}
	return result
}

func hexEncode(value []byte) string {
	return hex.EncodeToString(value)
}

func subtleCompare(left, right []byte) int {
	if len(left) != len(right) {
		return 0
	}
	return subtle.ConstantTimeCompare(left, right)
}
