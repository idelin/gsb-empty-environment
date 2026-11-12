package webhook

import "errors"

var (
	// ErrNonHTTPS 表示回调地址不是 https://。
	ErrNonHTTPS = errors.New("webhook: callback URL must use https")
	// ErrMissingHost 表示回调 URL 没有主机部分。
	ErrMissingHost = errors.New("webhook: callback URL has no host")
	// ErrBlockedAddress 表示主机解析到了回环、私网等禁止投递的地址。
	ErrBlockedAddress = errors.New("webhook: host resolves to a blocked loopback/private address")
	// ErrRedirect 表示对端返回了重定向；握手过程禁止跟随重定向。
	ErrRedirect = errors.New("webhook: redirects are not followed")
	// ErrUnexpectedStatus 表示对端返回了 2xx 之外的状态码。
	ErrUnexpectedStatus = errors.New("webhook: unexpected validation response status")
	// ErrMissingAllowedOrigin 表示响应缺少 WebHook-Allowed-Origin 头。
	ErrMissingAllowedOrigin = errors.New("webhook: missing WebHook-Allowed-Origin header")
	// ErrOriginNotAllowed 表示 WebHook-Allowed-Origin 既不是发送方 origin 也不是 "*"。
	ErrOriginNotAllowed = errors.New("webhook: request origin is not allowed")
	// ErrMissingAllowedRate 表示响应缺少 WebHook-Allowed-Rate 头。
	ErrMissingAllowedRate = errors.New("webhook: missing WebHook-Allowed-Rate header")
	// ErrInvalidAllowedRate 表示 WebHook-Allowed-Rate 不是正整数或 "*"。
	ErrInvalidAllowedRate = errors.New("webhook: invalid WebHook-Allowed-Rate header")
	// ErrHostMismatch 表示验证响应来自与登记地址不同的主机。
	ErrHostMismatch = errors.New("webhook: validation response host does not match registration host")
	// ErrSubscriptionNotFound 表示注册表中不存在目标订阅。
	ErrSubscriptionNotFound = errors.New("webhook: subscription not found")
	// ErrSubscriptionExpired 表示订阅距上次验证已超过有效期，投递已暂停。
	ErrSubscriptionExpired = errors.New("webhook: subscription validation expired, delivery paused")
	// ErrExistingSubscription 表示目标地址已经登记过。
	ErrExistingSubscription = errors.New("webhook: subscription already registered")
)
