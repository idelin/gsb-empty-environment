package hlctxn

import (
	"errors"
	"time"
)

// DrainIntents 扫描并解析全部残留意图（LazyResolution 模式测试/运维使用）。
// 返回被解析的意图数量。
func (c *Cluster) DrainIntents() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	resolved := 0
	for _, n := range c.nodes {
		for key, ks := range n.store {
			if ks.intent != nil {
				before := c.stats.IntentResolved
				c.resolveIntentLocked(ks, n.ID, key)
				if c.stats.IntentResolved > before {
					resolved++
				}
			}
		}
	}
	return resolved
}

// GetWithRestart 在事务内读取键；遇到 ReadRestartError 时按新 readTS
// 自动重读，直到成功或超出重试预算。同一节点的重启由引擎保证只发生一次，
// 因此重试次数受节点数上界约束，不会死循环。
func (txn *Transaction) GetWithRestart(key string) (value string, ok bool, restarts int, err error) {
	for {
		value, ok, err = txn.Get(key)
		if err == nil {
			return value, ok, restarts, nil
		}
		var rr *ReadRestartError
		if !errors.As(err, &rr) {
			return "", false, restarts, err
		}
		restarts++
		if restarts > maxAutoRetries {
			return "", false, restarts, ErrRetryLimitExceeded
		}
	}
}

// RunWithRetry 用 begin 开启事务并执行 body；ReadRestartError/ConflictError
// 触发退避后以新事务整体重试，直到 body 成功提交或超出预算。
// body 内返回的非事务错误会被原样透传（不会重试）。
func (c *Cluster) RunWithRetry(
	coordinator int,
	priority int32,
	backoff time.Duration,
	body func(txn *Transaction) error,
) (*Transaction, error) {
	for attempt := 0; attempt <= maxAutoRetries; attempt++ {
		txn := c.Begin(coordinator, priority)
		err := body(txn)
		if err == nil {
			_, err = txn.Commit()
			if err == nil {
				return txn, nil
			}
		}
		var rr *ReadRestartError
		var cf *ConflictError
		if errors.As(err, &rr) || errors.As(err, &cf) ||
			errors.Is(err, ErrAborted) || errors.Is(err, ErrTooManyRestarts) {
			_ = txn.Abort()
			if backoff > 0 {
				time.Sleep(backoff)
			}
			continue
		}
		_ = txn.Abort()
		return nil, err
	}
	return nil, ErrRetryLimitExceeded
}
