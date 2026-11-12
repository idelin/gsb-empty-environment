package webhook

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Rate 表示 WebHook-Allowed-Rate 头允许的投递速率。
//
// Wildcard 为 true 时表示对端接受任意速率（头值为 "*"）；
// 否则 RequestsPerSecond 为对端声明的正整数上限（次/秒）。
type Rate struct {
	RequestsPerSecond int
	Wildcard          bool
}

// ParseAllowedRate 解析 WebHook-Allowed-Rate 头。
// 合法值为 "*" 或一个正整数，其他值一律视为拒绝。
func ParseAllowedRate(value string) (Rate, error) {
	value = strings.TrimSpace(value)
	if value == "*" {
		return Rate{Wildcard: true}, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return Rate{}, fmt.Errorf("%w: %q", ErrInvalidAllowedRate, value)
	}
	return Rate{RequestsPerSecond: n}, nil
}

// String 按线上协议的形式渲染速率值。
func (r Rate) String() string {
	if r.Wildcard {
		return "*"
	}
	return strconv.Itoa(r.RequestsPerSecond)
}

// MinRate 取发送方请求速率与订阅方允许速率的较小值。
// "*" 代表不限制，因此另一方为具体值时取具体值；两者都为 "*" 时返回 "*"。
func MinRate(requested, allowed Rate) (Rate, error) {
	if requested.Wildcard && allowed.Wildcard {
		return Rate{Wildcard: true}, nil
	}
	if requested.Wildcard {
		return allowed, nil
	}
	if allowed.Wildcard {
		return requested, nil
	}
	if requested.RequestsPerSecond <= 0 || allowed.RequestsPerSecond <= 0 {
		return Rate{}, errors.New("webhook: rate must be positive")
	}
	if requested.RequestsPerSecond < allowed.RequestsPerSecond {
		return requested, nil
	}
	return allowed, nil
}
