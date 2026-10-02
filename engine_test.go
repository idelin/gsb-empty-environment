package rulecache

import (
	"fmt"
	"testing"
	"time"
)

const testTenant = "tenant-1"

func setup(t *testing.T, n int) (*DimensionStore, *Engine) {
	t.Helper()
	store := NewDimensionStore()
	engine := NewEngine(store, NewCache())
	month := time.Now().Format("01")
	store.Set(DimKey(testTenant+":config:campaign"), map[string]string{"birthday_bonus_enabled": "true"})
	for i := 0; i < n; i++ {
		u := fmt.Sprintf("U%d", i)
		store.Set(DimKey(testTenant+":user:"+u+":member"), map[string]string{"level": "NORMAL"})
		store.Set(DimKey(testTenant+":user:"+u+":profile"), map[string]string{"birthday": "1990-" + month + "-15"})
	}
	return store, engine
}

func warm(t *testing.T, e *Engine, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := e.Evaluate(VIPBirthdayBonusRule{}, testTenant, fmt.Sprintf("U%d", i)); err != nil {
			t.Fatal(err)
		}
	}
}

// 验收边界：U 的会员等级变更时，仅 U 的缓存失效，其余用户缓存保持热态。
func TestPreciseInvalidation(t *testing.T) {
	const n = 10000
	store, engine := setup(t, n)
	warm(t, engine, n)

	if s := engine.Stats(); s.Size != n {
		t.Fatalf("预热后缓存项 = %d, 期望 %d", s.Size, n)
	}

	// U42 会员等级变更
	store.Set(DimKey(testTenant+":user:U42:member"), map[string]string{"level": "VIP"})

	s := engine.Stats()
	if s.Invalidations != 1 {
		t.Fatalf("失效缓存数 = %d, 期望精确为 1", s.Invalidations)
	}
	if s.Size != n-1 {
		t.Fatalf("剩余缓存项 = %d, 期望 %d", s.Size, n-1)
	}

	// 其余 9999 个用户全部命中（零失效），仅 U42 未命中重算
	hitsBefore := s.Hits
	warm(t, engine, n)
	s = engine.Stats()
	if got := s.Hits - hitsBefore; got != n-1 {
		t.Fatalf("复查询命中 = %d, 期望 %d（其他用户缓存保持热态）", got, n-1)
	}
}

// 失效后重新计算必须反映新维度值。
func TestResultReflectsUpdate(t *testing.T) {
	store, engine := setup(t, 1)
	v, err := engine.Evaluate(VIPBirthdayBonusRule{}, testTenant, "U0")
	if err != nil {
		t.Fatal(err)
	}
	if v != 50 {
		t.Fatalf("普通会员积分 = %v, 期望 50", v)
	}

	store.Set(DimKey(testTenant+":user:U0:member"), map[string]string{"level": "VIP"})
	v, err = engine.Evaluate(VIPBirthdayBonusRule{}, testTenant, "U0")
	if err != nil {
		t.Fatal(err)
	}
	if v != 100 {
		t.Fatalf("升级 VIP 后积分 = %v, 期望 100", v)
	}
}

// 无缓存依赖的维度更新，不得引起任何失效。
func TestUnrelatedDimensionUpdate(t *testing.T) {
	const n = 100
	store, engine := setup(t, n)
	warm(t, engine, n)

	store.Set(DimKey(testTenant+":user:U7:address"), map[string]string{"city": "Shanghai"})

	if s := engine.Stats(); s.Invalidations != 0 || s.Size != n {
		t.Fatalf("无关维度更新导致失效 %d 项, 期望 0", s.Invalidations)
	}
}

// 共享维度（租户活动开关）更新时，所有依赖它的缓存都应失效 —— 验证反向索引的完整性。
func TestSharedDimensionPropagation(t *testing.T) {
	const n = 500
	store, engine := setup(t, n)
	warm(t, engine, n)

	store.Set(DimKey(testTenant+":config:campaign"), map[string]string{"birthday_bonus_enabled": "false"})

	if s := engine.Stats(); s.Invalidations != n || s.Size != 0 {
		t.Fatalf("共享维度更新后失效 = %d, 期望 %d", s.Invalidations, n)
	}
	v, err := engine.Evaluate(VIPBirthdayBonusRule{}, testTenant, "U0")
	if err != nil {
		t.Fatal(err)
	}
	if v != 0 {
		t.Fatalf("活动关闭后积分 = %v, 期望 0", v)
	}
}

// 失效重算后反向索引应重建，再次更新同一维度依然精准。
func TestIndexRebuiltAfterRecompute(t *testing.T) {
	store, engine := setup(t, 3)
	warm(t, engine, 3)

	store.Set(DimKey(testTenant+":user:U1:member"), map[string]string{"level": "VIP"})
	warm(t, engine, 3) // U1 重算并重建索引

	store.Set(DimKey(testTenant+":user:U1:member"), map[string]string{"level": "SVIP"})
	s := engine.Stats()
	if s.Invalidations != 2 {
		t.Fatalf("累计失效 = %d, 期望 2（每次各精准失效 1 项）", s.Invalidations)
	}
	v, _ := engine.Evaluate(VIPBirthdayBonusRule{}, testTenant, "U1")
	if v != 200 {
		t.Fatalf("升级 SVIP 后积分 = %v, 期望 200", v)
	}
}
