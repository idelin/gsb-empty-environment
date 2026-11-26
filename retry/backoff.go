package retry

import (
	"hash/fnv"
	"math"
	"time"
)

// Backoff 计算第 attempt 次重试前（attempt 从 1 开始）的退避等待时长。
//
// 采用指数退避 base * 2^(attempt-1)，并叠加确定性全抖动（full jitter）：
//
//	jitter = [0, deterministicFactor * backoff)
//
// 抖动由 requestID 与 attempt 的 FNV-1a 哈希确定性产生，同一请求在任意副本上
// 重放都得到相同序列，避免随机抖动导致的不可复现与同跳重试同步（thundering herd）。
// 当 deterministicFactor <= 0 时不附加抖动。
func Backoff(base time.Duration, multiplier, deterministicFactor float64, requestID string, attempt int) time.Duration {
	if base <= 0 || attempt <= 0 {
		return 0
	}
	mult := multiplier
	if mult <= 0 {
		mult = 2
	}
	backoff := float64(base) * math.Pow(mult, float64(attempt-1))
	if deterministicFactor <= 0 {
		return time.Duration(backoff)
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(requestID))
	_, _ = h.Write([]byte{byte(attempt), byte(attempt >> 8), byte(attempt >> 16)})
	frac := float64(h.Sum64()%1_000_000) / 1_000_000.0
	return time.Duration(backoff * (1 + deterministicFactor*(frac*2-1)))
}
