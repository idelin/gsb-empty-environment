// Package lease 实现带版本约束的配置信息租约绑定器。
//
// 集群动态配置分发与生效机制：
//   - 工作节点向配置中心租用配置版本（签发租约，租约绑定配置版本号）；
//   - 配置更新时，配置中心撤销全部旧租约，并向在线节点下发新配置与新租约凭证；
//   - 节点只有在持有有效（未过期、未被撤销、版本与所持配置一致）租约时才对外接客；
//   - 脱网节点收不到撤销通知，但旧租约会自然过期，节点据此主动停止接收流量。
package lease

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	// ErrNoValidLease 节点没有有效租约，拒绝对外服务。
	ErrNoValidLease = errors.New("lease: no valid config lease, refuse to serve")
	// ErrLeaseRevoked 租约已被配置中心撤销。
	ErrLeaseRevoked = errors.New("lease: token revoked by config center")
	// ErrLeaseExpired 租约已过期。
	ErrLeaseExpired = errors.New("lease: token expired")
	// ErrVersionStale 租约绑定的配置版本已落后于配置中心当前版本。
	ErrVersionStale = errors.New("lease: token bound to a stale config version")
	// ErrUnknownToken 租约凭证不存在。
	ErrUnknownToken = errors.New("lease: unknown token")
	// ErrNodeOffline 节点脱网，配置中心无法向其推送。
	ErrNodeOffline = errors.New("lease: node is offline")
)

// Config 是一份带版本号的集群动态配置。
type Config struct {
	Version uint64
	Data    map[string]string
}

func (c Config) clone() Config {
	d := make(map[string]string, len(c.Data))
	for k, v := range c.Data {
		d[k] = v
	}
	return Config{Version: c.Version, Data: d}
}

// Lease 是配置中心签发的租约凭证，绑定到某个配置版本。
type Lease struct {
	Token     string
	Version   uint64
	IssuedAt  time.Time
	ExpiresAt time.Time
}

func newToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// Center 是配置中心：持有当前配置版本，负责签发、续期、撤销租约并下发配置。
type Center struct {
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Time
	config  Config
	leases  map[string]Lease // token -> lease（在册租约）
	revoked map[string]bool  // 已撤销的 token
}

// NewCenter 创建配置中心。ttl 为租约时长，initial 为初始配置数据（版本从 1 开始）。
func NewCenter(ttl time.Duration, initial map[string]string) *Center {
	return newCenter(ttl, initial, time.Now)
}

func newCenter(ttl time.Duration, initial map[string]string, now func() time.Time) *Center {
	return &Center{
		ttl:     ttl,
		now:     now,
		config:  Config{Version: 1, Data: initial}.clone(),
		leases:  make(map[string]Lease),
		revoked: make(map[string]bool),
	}
}

// CurrentConfig 返回配置中心当前配置。
func (c *Center) CurrentConfig() Config {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.config.clone()
}

// Issue 为当前配置版本签发一份新租约，返回配置快照与租约凭证。
func (c *Center) Issue() (Config, Lease) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.config.clone(), c.issueLocked()
}

func (c *Center) issueLocked() Lease {
	l := Lease{
		Token:     newToken(),
		Version:   c.config.Version,
		IssuedAt:  c.now(),
		ExpiresAt: c.now().Add(c.ttl),
	}
	c.leases[l.Token] = l
	return l
}

// validateLocked 校验 token 的有效性：在册、未撤销、未过期、版本与当前配置一致。
func (c *Center) validateLocked(token string) (Lease, error) {
	if c.revoked[token] {
		return Lease{}, ErrLeaseRevoked
	}
	l, ok := c.leases[token]
	if !ok {
		return Lease{}, ErrUnknownToken
	}
	if l.Version != c.config.Version {
		return Lease{}, ErrVersionStale
	}
	if !c.now().Before(l.ExpiresAt) {
		return Lease{}, ErrLeaseExpired
	}
	return l, nil
}

// Validate 校验租约凭证是否仍然有效（供节点或网关向配置中心核实）。
func (c *Center) Validate(token string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.validateLocked(token)
	return err
}

// Renew 续期租约。旧 token 作废，返回绑定当前版本的新租约；
// 若旧租约已失效（撤销/过期/版本落后），返回错误，调用方应停止服务。
func (c *Center) Renew(token string) (Config, Lease, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.validateLocked(token); err != nil {
		return Config{}, Lease{}, err
	}
	delete(c.leases, token)
	c.revoked[token] = true // 旧凭证一次性使用，续期后即作废
	l := c.issueLocked()
	return c.config.clone(), l, nil
}

// Update 修改配置：版本号 +1，并撤销此前签发的全部租约。
// 返回新配置；调用方随后应通过 Publish 把新配置与新租约下发到各节点。
func (c *Center) Update(data map[string]string) Config {
	c.mu.Lock()
	defer c.mu.Unlock()
	for token := range c.leases {
		c.revoked[token] = true
		delete(c.leases, token)
	}
	c.config = Config{Version: c.config.Version + 1, Data: data}.clone()
	return c.config.clone()
}

// Publish 向单个节点推送当前配置与新租约凭证。节点脱网时返回 ErrNodeOffline，
// 该节点只能等待旧租约自然过期后自行停止服务。
func (c *Center) Publish(n *Node) (err error) {
	cfg, l := c.Issue()
	defer func() {
		if r := recover(); r != nil {
			err = ErrNodeOffline
		}
	}()
	return n.onPush(cfg, l)
}

// Node 是工作节点：只有持有有效配置租约时才对外接客。
type Node struct {
	mu     sync.Mutex
	name   string
	center *Center
	now    func() time.Time
	online bool
	config Config
	lease  *Lease
	served int
}

// NewNode 创建工作节点（默认在线）。
func NewNode(name string, c *Center) *Node {
	return &Node{name: name, center: c, now: time.Now, online: true}
}

// Name 返回节点名。
func (n *Node) Name() string { return n.name }

// SetOnline 模拟节点联网/脱网。
func (n *Node) SetOnline(online bool) {
	n.mu.Lock()
	n.online = online
	n.mu.Unlock()
}

// Start 节点启动：向配置中心租用当前配置版本。
func (n *Node) Start() error {
	cfg, l := n.center.Issue()
	n.mu.Lock()
	defer n.mu.Unlock()
	n.config = cfg
	n.lease = &l
	return nil
}

// onPush 接收配置中心推送的新配置与新租约凭证。
func (n *Node) onPush(cfg Config, l Lease) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.online {
		return ErrNodeOffline
	}
	n.config = cfg
	n.lease = &l
	return nil
}

// Renew 在租约到期前向配置中心续期。续期失败（租约已失效）时清空本地租约，
// 节点随即停止对外服务。
func (n *Node) Renew() error {
	n.mu.Lock()
	if n.lease == nil {
		n.mu.Unlock()
		return ErrNoValidLease
	}
	token := n.lease.Token
	n.mu.Unlock()

	cfg, l, err := n.center.Renew(token)
	if err != nil {
		n.mu.Lock()
		if n.lease != nil && n.lease.Token == token {
			n.lease = nil // 租约已被中心判定失效，本地立即作废
		}
		n.mu.Unlock()
		return err
	}
	n.mu.Lock()
	n.config = cfg
	n.lease = &l
	n.mu.Unlock()
	return nil
}

// leaseValidLocked 本地判定租约有效性：存在、未过期、且版本与所持配置一致。
// 脱网节点无法联系配置中心，只能依赖该本地判定——租约自然过期后即拒绝服务。
func (n *Node) leaseValidLocked() bool {
	return n.lease != nil &&
		n.lease.Version == n.config.Version &&
		n.now().Before(n.lease.ExpiresAt)
}

// Serve 对外接客。仅在持有有效租约时处理请求，否则返回 ErrNoValidLease。
func (n *Node) Serve(request string) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.leaseValidLocked() {
		return "", ErrNoValidLease
	}
	n.served++
	return fmt.Sprintf("node=%s req=%q configVersion=%d dbPassword=%s",
		n.name, request, n.config.Version, n.config.Data["db_password"]), nil
}

// Status 返回节点当前状态描述（用于演示与测试断言）。
func (n *Node) Status() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.leaseValidLocked() {
		return fmt.Sprintf("%s: NOT SERVING (no valid lease, configVersion=%d, served=%d)",
			n.name, n.config.Version, n.served)
	}
	return fmt.Sprintf("%s: serving (configVersion=%d, leaseExpiresIn=%s, served=%d)",
		n.name, n.config.Version, n.lease.ExpiresAt.Sub(n.now()).Round(time.Millisecond), n.served)
}
