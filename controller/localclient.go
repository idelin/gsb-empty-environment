package controller

import (
	"time"

	"leasefed/witness"
)

// LocalClient 是进程内直连法官核心的 Client 实现,
// 适用于单进程部署、演示与测试;跨进程部署请使用 HTTPClient。
type LocalClient struct {
	Core *witness.Core
}

func (c *LocalClient) Acquire(holder string, ttl time.Duration) (witness.Lease, bool, error) {
	l, ok := c.Core.Acquire(holder, ttl)
	return l, ok, nil
}

func (c *LocalClient) Renew(holder string, epoch uint64, ttl time.Duration) (witness.Lease, bool, error) {
	l, ok := c.Core.Renew(holder, epoch, ttl)
	return l, ok, nil
}
