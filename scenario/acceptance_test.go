package scenario

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"leasefed/controller"
	"leasefed/store"
	"leasefed/witness"
)

// link 模拟机房到法官的物理链路,cut=true 即"光纤挖断"。
type link struct {
	inner controller.Client
	cut   atomic.Bool
}

func (l *link) Acquire(holder string, ttl time.Duration) (witness.Lease, bool, error) {
	if l.cut.Load() {
		return witness.Lease{}, false, errors.New("光纤挖断: 链路不可达")
	}
	return l.inner.Acquire(holder, ttl)
}

func (l *link) Renew(holder string, epoch uint64, ttl time.Duration) (witness.Lease, bool, error) {
	if l.cut.Load() {
		return witness.Lease{}, false, errors.New("光纤挖断: 链路不可达")
	}
	return l.inner.Renew(holder, epoch, ttl)
}

// writeThrough 模拟机房业务写:先取 fencing token,再携带 token 写存储层。
func writeThrough(c *controller.Controller, s *store.Store, key, val string) error {
	tok, ok := c.FencingToken()
	if !ok {
		return controller.ErrNotPrimary
	}
	return s.Put(tok, key, val)
}

// TestAcceptance_FailoverAcrossDatacenters 完整走一遍验收边界:
//
//	机房1 主 / 机房2 备 -> 光纤挖断 -> 法官确认机房1失联并向机房2颁发晋升租约
//	-> 机房1恢复后检测到凭证失效,自动切换为只读备机,全程无双写。
func TestAcceptance_FailoverAcrossDatacenters(t *testing.T) {
	// 第三方机房部署的法官(进程内直连;跨进程部署时换成 HTTPClient 即可,
	// HTTP 传输层的等价性由 controller 包的 http 测试保证)
	w := witness.NewCore()

	const ttl = 150 * time.Millisecond
	dc1Link := &link{inner: &controller.LocalClient{Core: w}}
	dc2Link := &link{inner: &controller.LocalClient{Core: w}}
	dc1 := controller.New("dc1", dc1Link, ttl)
	dc2 := controller.New("dc2", dc2Link, ttl)
	storage := store.New() // 全局存储层(校验 fencing token 的最后一道闸)

	// --- 阶段 1: 机房1 为主,机房2 为备 ---
	dc1.Tick()
	dc2.Tick()
	if dc1.State() != controller.StatePrimary || dc2.State() != controller.StateStandby {
		t.Fatalf("初始角色错误: dc1=%v dc2=%v", dc1.State(), dc2.State())
	}
	if err := writeThrough(dc1, storage, "order-1", "created-by-dc1"); err != nil {
		t.Fatalf("主机房写入应成功: %v", err)
	}
	if err := writeThrough(dc2, storage, "order-x", "bad"); !errors.Is(err, controller.ErrNotPrimary) {
		t.Fatalf("备机写入必须被拒: %v", err)
	}
	dc1Epoch := dc1.Lease().Epoch

	// --- 阶段 2: 光纤挖断,机房1 整体失联(掉电,不再有任何 Tick) ---
	dc1Link.cut.Store(true)
	time.Sleep(2 * ttl) // 等待法官侧租约过期,即"法官确认机房1真正失联"

	// --- 阶段 3: 法官向机房2 颁发晋升租约 ---
	dc2.Tick()
	if dc2.State() != controller.StatePrimary {
		t.Fatal("机房2 应被晋升为可写主机")
	}
	promoted := dc2.Lease()
	if promoted.Epoch <= dc1Epoch {
		t.Fatalf("晋升租约 epoch 必须更大: dc1=%d dc2=%d", dc1Epoch, promoted.Epoch)
	}
	if err := writeThrough(dc2, storage, "order-2", "created-by-dc2"); err != nil {
		t.Fatalf("新主机房写入应成功: %v", err)
	}

	// 失联期间机房1 若仍有迟到写入(带着旧 fencing token),存储层必须拒绝
	if err := storage.Put(dc1Epoch, "order-3", "stale-write-from-dc1"); !errors.Is(err, store.ErrStaleFencingToken) {
		t.Fatalf("旧主机房的过时写入必须被存储层拒绝: %v", err)
	}

	// --- 阶段 4: 机房1 恢复,检测到凭证失效,自动切换为只读备机 ---
	dc1Link.cut.Store(false)
	dc1.Tick() // 用旧 epoch 续租 -> 法官拒绝 -> 自动降级
	if dc1.State() != controller.StateStandby {
		t.Fatalf("机房1 恢复后必须自动降级为只读备机, got %v", dc1.State())
	}
	if dc1.StepDownCount() != 1 {
		t.Fatalf("机房1 应记录一次降级, got %d", dc1.StepDownCount())
	}
	if err := writeThrough(dc1, storage, "order-4", "must-not-happen"); !errors.Is(err, controller.ErrNotPrimary) {
		t.Fatalf("降级后的机房1 写入必须被拒: %v", err)
	}

	// 机房1 作为备机重新竞选,只要机房2 租约有效就必须一直落选
	dc2.Tick() // 机房2 正常续租
	dc1.Tick()
	if dc1.State() != controller.StateStandby || dc2.State() != controller.StatePrimary {
		t.Fatalf("角色必须稳定: dc1=%v dc2=%v", dc1.State(), dc2.State())
	}

	// --- 终态校验: 全局无脏写、无双主痕迹 ---
	if v, _ := storage.Get("order-1"); v != "created-by-dc1" {
		t.Fatalf("机房1 任期内的合法数据应保留, got %q", v)
	}
	if v, _ := storage.Get("order-2"); v != "created-by-dc2" {
		t.Fatalf("机房2 晋升后的数据应存在, got %q", v)
	}
	if _, ok := storage.Get("order-3"); ok {
		t.Fatal("过时 fencing token 的写入不应落盘")
	}
	if _, ok := storage.Get("order-4"); ok {
		t.Fatal("只读备机的写入不应落盘")
	}
	if storage.Len() != 2 {
		t.Fatalf("存储层应恰好有 2 条合法记录, got %d", storage.Len())
	}
}
