// Package witness 实现轻量级第三方"法官"(Witness)。
//
// 法官是跨机房全局唯一的租约仲裁者:任意时刻全系统只存在一份有效租约,
// 每份租约携带单调递增的 fencing token(Epoch),用于在存储层彻底杜绝
// 双机房双写(split-brain)。
package witness

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Lease 是法官颁发的一份全局写租约。
type Lease struct {
	Holder string    `json:"holder"` // 租约持有者(机房标识)
	Epoch  uint64    `json:"epoch"`  // fencing token,全局单调递增
	Expiry time.Time `json:"expiry"` // 租约到期时刻(法官本地时钟)
}

// Core 是法官的仲裁核心,可单进程内使用,也可通过 Handler 暴露为 HTTP 服务。
type Core struct {
	mu    sync.Mutex
	now   func() time.Time // 可注入时钟,便于测试
	epoch uint64
	lease *Lease // 当前有效租约;nil 表示暂无
}

// NewCore 创建一个法官核心。
func NewCore() *Core {
	return &Core{now: time.Now}
}

// SetClock 注入自定义时钟(仅用于测试)。
func (c *Core) SetClock(now func() time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

// Acquire 申请租约。仅当当前无有效租约(不存在或已过期),
// 或申请者本身就是当前持有者时,才会颁发新租约。
// 每次颁发都会递增 Epoch,保证 fencing token 全局单调。
func (c *Core) Acquire(holder string, ttl time.Duration) (Lease, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.lease != nil && now.Before(c.lease.Expiry) && c.lease.Holder != holder {
		return *c.lease, false // 他人持有且未过期,拒绝
	}
	c.epoch++
	l := Lease{Holder: holder, Epoch: c.epoch, Expiry: now.Add(ttl)}
	c.lease = &l
	return l, true
}

// Renew 续租。仅当 holder+epoch 与当前租约完全一致且租约未过期时成功。
// 机房失联恢复后,其旧 epoch 已被法官作废(新租约已颁发给他人),续租会被拒绝,
// 这就是"凭证已失效"的判定依据。
func (c *Core) Renew(holder string, epoch uint64, ttl time.Duration) (Lease, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.lease == nil || c.lease.Holder != holder || c.lease.Epoch != epoch || !now.Before(c.lease.Expiry) {
		if c.lease != nil {
			return *c.lease, false
		}
		return Lease{}, false
	}
	c.lease.Expiry = now.Add(ttl)
	return *c.lease, true
}

// Status 返回当前租约视图(可能已过期,调用方自行判断)。
func (c *Core) Status() (Lease, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lease == nil {
		return Lease{}, false
	}
	return *c.lease, true
}

// ---- HTTP 暴露 ----

type response struct {
	Granted bool   `json:"granted"`
	Lease   *Lease `json:"lease,omitempty"`
}

// Handler 将法官核心暴露为 HTTP 服务:
//
//	GET /acquire?holder=dc1&ttl_ms=3000
//	GET /renew?holder=dc1&epoch=7&ttl_ms=3000
//	GET /status
func (c *Core) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/acquire", func(w http.ResponseWriter, r *http.Request) {
		holder := r.URL.Query().Get("holder")
		ttl, err := ttlParam(r)
		if holder == "" || err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		lease, ok := c.Acquire(holder, ttl)
		writeJSON(w, response{Granted: ok, Lease: &lease})
	})
	mux.HandleFunc("/renew", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		holder := q.Get("holder")
		epoch, err := strconv.ParseUint(q.Get("epoch"), 10, 64)
		ttl, terr := ttlParam(r)
		if holder == "" || err != nil || terr != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		lease, ok := c.Renew(holder, epoch, ttl)
		writeJSON(w, response{Granted: ok, Lease: &lease})
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		lease, ok := c.Status()
		if !ok {
			writeJSON(w, response{})
			return
		}
		writeJSON(w, response{Granted: true, Lease: &lease})
	})
	return mux
}

func ttlParam(r *http.Request) (time.Duration, error) {
	ms, err := strconv.Atoi(r.URL.Query().Get("ttl_ms"))
	if err != nil || ms <= 0 {
		return 0, fmt.Errorf("invalid ttl_ms")
	}
	return time.Duration(ms) * time.Millisecond, nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
