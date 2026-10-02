package controller

import (
	"errors"
	"testing"
	"time"

	"leasefed/witness"
)

// fakeClient 用内存法官核心模拟到法官的通信,down=true 时模拟光纤挖断。
type fakeClient struct {
	core *witness.Core
	down bool
}

var errFiberCut = errors.New("光纤挖断: 到法官的链路不可达")

func (f *fakeClient) Acquire(holder string, ttl time.Duration) (witness.Lease, bool, error) {
	if f.down {
		return witness.Lease{}, false, errFiberCut
	}
	l, ok := f.core.Acquire(holder, ttl)
	return l, ok, nil
}

func (f *fakeClient) Renew(holder string, epoch uint64, ttl time.Duration) (witness.Lease, bool, error) {
	if f.down {
		return witness.Lease{}, false, errFiberCut
	}
	l, ok := f.core.Renew(holder, epoch, ttl)
	return l, ok, nil
}

func TestCampaignAndRenew(t *testing.T) {
	core := witness.NewCore()
	c := New("dc1", &fakeClient{core: core}, time.Minute)

	if c.State() != StateStandby {
		t.Fatal("初始应为只读备机")
	}
	c.Tick() // 竞选
	if c.State() != StatePrimary {
		t.Fatal("竞选后应为主机房")
	}
	tok, ok := c.FencingToken()
	if !ok || tok != 1 {
		t.Fatalf("主机房应持有 fencing token=1, got %d ok=%v", tok, ok)
	}
	c.Tick() // 续租
	if c.State() != StatePrimary {
		t.Fatal("续租成功应保持主机房")
	}
}

func TestStandbyCannotWinWhileLeaseValid(t *testing.T) {
	core := witness.NewCore()
	dc1 := New("dc1", &fakeClient{core: core}, time.Minute)
	dc2 := New("dc2", &fakeClient{core: core}, time.Minute)

	dc1.Tick()
	dc2.Tick() // dc1 租约有效,dc2 竞选失败
	if dc2.State() != StateStandby {
		t.Fatal("租约有效期内备机不应晋升")
	}
	if _, ok := dc2.FencingToken(); ok {
		t.Fatal("备机不应持有有效 fencing token")
	}
}

func TestStepDownWhenCredentialInvalidated(t *testing.T) {
	now := time.Now()
	core := witness.NewCore()
	core.SetClock(func() time.Time { return now })
	c := New("dc1", &fakeClient{core: core}, time.Minute)
	c.Tick()

	// 模拟 dc1 整体掉电:租约在法官侧过期,dc2 接管
	now = now.Add(2 * time.Minute)
	if _, ok := core.Acquire("dc2", time.Minute); !ok {
		t.Fatal("前置条件: dc2 应已接管")
	}
	c.Tick() // dc1 恢复后第一次续租 -> 凭证失效 -> 降级
	if c.State() != StateStandby {
		t.Fatal("凭证失效后必须自动降级为只读备机")
	}
	if c.StepDownCount() != 1 {
		t.Fatalf("应记录一次降级, got %d", c.StepDownCount())
	}
	if _, ok := c.FencingToken(); ok {
		t.Fatal("降级后不应再持有有效 fencing token")
	}
}

func TestStepDownOnLocalExpiryDuringPartition(t *testing.T) {
	core := witness.NewCore()
	fc := &fakeClient{core: core}
	c := New("dc1", fc, 50*time.Millisecond)
	c.Tick()
	if c.State() != StatePrimary {
		t.Fatal("前置条件: dc1 应为主机房")
	}

	fc.down = true                    // 光纤挖断
	time.Sleep(60 * time.Millisecond) // 等本地租约过期
	c.Tick()                          // 续租失败且本地已过期 -> 安全降级
	if c.State() != StateStandby {
		t.Fatal("失联且租约过期后必须主动降级,宁可不可写也绝不双写")
	}
}

func TestPrimarySurvivesTransientErrorBeforeExpiry(t *testing.T) {
	core := witness.NewCore()
	fc := &fakeClient{core: core}
	c := New("dc1", fc, time.Minute)
	c.Tick()

	fc.down = true
	c.Tick() // 租约远未到期,短暂故障不应降级
	if c.State() != StatePrimary {
		t.Fatal("租约未到期时的瞬时故障不应导致降级")
	}
	fc.down = false
	c.Tick() // 恢复后续租成功
	if c.State() != StatePrimary {
		t.Fatal("链路恢复后应继续保持主机房")
	}
}
