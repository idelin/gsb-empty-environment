// Package controller 实现部署在每个机房的租约控制器。
//
// 状态机:
//
//	Standby(只读备机) --Acquire 成功--> Primary(可写主机)
//	Primary --续租被拒(凭证失效)或租约本地到期且无法续租--> Standby
//
// 控制器只在 Primary 且租约本地未过期时放行写请求,并把 Epoch 作为
// fencing token 一并交给存储层校验。
package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"leasefed/witness"
)

// State 是机房的角色状态。
type State int

const (
	StateStandby State = iota // 只读备机
	StatePrimary              // 可写主机
)

func (s State) String() string {
	if s == StatePrimary {
		return "PRIMARY(可写)"
	}
	return "STANDBY(只读)"
}

var (
	ErrNotPrimary   = errors.New("当前不是主机房,拒绝写入(只读)")
	ErrLeaseExpired = errors.New("本地租约已过期,拒绝写入")
)

// Client 抽象到法官的通信,便于测试时注入"光纤挖断"等故障。
type Client interface {
	Acquire(holder string, ttl time.Duration) (witness.Lease, bool, error)
	Renew(holder string, epoch uint64, ttl time.Duration) (witness.Lease, bool, error)
}

// Controller 是单个机房的租约控制器。
type Controller struct {
	id     string
	client Client
	ttl    time.Duration
	now    func() time.Time

	mu        sync.Mutex
	state     State
	lease     witness.Lease
	onChange  func(from, to State)
	stepDownN int
}

// New 创建控制器。ttl 为每次申请/续租的租约时长。
func New(id string, client Client, ttl time.Duration) *Controller {
	return &Controller{id: id, client: client, ttl: ttl, now: time.Now, state: StateStandby}
}

// SetClock 注入时钟(仅测试用)。
func (c *Controller) SetClock(now func() time.Time) { c.now = now }

// OnStateChange 注册状态变更回调(仅测试/演示用)。
func (c *Controller) OnStateChange(f func(from, to State)) { c.onChange = f }

// State 返回当前角色。
func (c *Controller) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Lease 返回当前持有的租约(仅 Primary 时有意义)。
func (c *Controller) Lease() witness.Lease {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lease
}

// Tick 执行一轮状态机推进:备机尝试竞选,主机尝试续租。
// Run 周期性调用它;测试可直接调用以实现确定性推进。
func (c *Controller) Tick() {
	c.mu.Lock()
	state, lease := c.state, c.lease
	c.mu.Unlock()

	if state == StatePrimary {
		c.renewOnce(lease)
		return
	}
	// 备机竞选
	l, ok, err := c.client.Acquire(c.id, c.ttl)
	if err != nil || !ok {
		return // 竞选失败,保持只读备机
	}
	c.mu.Lock()
	c.lease = l
	c.transitionLocked(StatePrimary)
	c.mu.Unlock()
}

func (c *Controller) renewOnce(lease witness.Lease) {
	l, ok, err := c.client.Renew(c.id, lease.Epoch, c.ttl)
	switch {
	case err == nil && ok:
		c.mu.Lock()
		c.lease = l
		c.mu.Unlock()
	case err == nil && !ok:
		// 法官明确拒绝:凭证已失效(租约已被颁发给他人),立即降级为只读备机。
		c.mu.Lock()
		c.transitionLocked(StateStandby)
		c.mu.Unlock()
	default:
		// 网络故障(如光纤挖断):若本地租约已到期,为安全起见主动降级,
		// 宁可不可写,也绝不冒双写风险。
		if !c.now().Before(lease.Expiry) {
			c.mu.Lock()
			c.transitionLocked(StateStandby)
			c.mu.Unlock()
		}
	}
}

func (c *Controller) transitionLocked(to State) {
	if c.state == to {
		return
	}
	from := c.state
	c.state = to
	if to == StateStandby {
		c.stepDownN++
	}
	if c.onChange != nil {
		c.onChange(from, to)
	}
}

// StepDownCount 返回累计降级次数(仅测试用)。
func (c *Controller) StepDownCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stepDownN
}

// FencingToken 返回当前可安全用于写入的 fencing token。
// 仅在 Primary 且本地租约未过期时返回 ok=true。
func (c *Controller) FencingToken() (uint64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state != StatePrimary || !c.now().Before(c.lease.Expiry) {
		return 0, false
	}
	return c.lease.Epoch, true
}

// Run 以 ttl/3 为周期推进状态机,直到 ctx 取消。
func (c *Controller) Run(ctx context.Context) {
	interval := c.ttl / 3
	if interval <= 0 {
		interval = 10 * time.Millisecond
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.Tick()
		}
	}
}

// ---- 到法官的 HTTP 客户端 ----

// HTTPClient 通过 HTTP 与法官通信。
type HTTPClient struct {
	Base string // 例如 http://127.0.0.1:9000
	HC   *http.Client
}

func (c *HTTPClient) httpClient() *http.Client {
	if c.HC != nil {
		return c.HC
	}
	return &http.Client{Timeout: 2 * time.Second}
}

func (c *HTTPClient) Acquire(holder string, ttl time.Duration) (witness.Lease, bool, error) {
	u := fmt.Sprintf("%s/acquire?holder=%s&ttl_ms=%d", c.Base, url.QueryEscape(holder), ttl.Milliseconds())
	return c.call(u)
}

func (c *HTTPClient) Renew(holder string, epoch uint64, ttl time.Duration) (witness.Lease, bool, error) {
	u := fmt.Sprintf("%s/renew?holder=%s&epoch=%s&ttl_ms=%d",
		c.Base, url.QueryEscape(holder), strconv.FormatUint(epoch, 10), ttl.Milliseconds())
	return c.call(u)
}

func (c *HTTPClient) call(u string) (witness.Lease, bool, error) {
	resp, err := c.httpClient().Get(u)
	if err != nil {
		return witness.Lease{}, false, err
	}
	defer resp.Body.Close()
	var r struct {
		Granted bool           `json:"granted"`
		Lease   *witness.Lease `json:"lease"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return witness.Lease{}, false, err
	}
	if r.Lease == nil {
		return witness.Lease{}, r.Granted, nil
	}
	return *r.Lease, r.Granted, nil
}
