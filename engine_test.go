package rulecache

import (
	"fmt"
	"sync"
	"testing"
)

func newEngineWithRule() *Engine {
	eng := New()
	eng.RegisterRule(RuleVIPBirthdayPoints, VIPBirthdayPoints)
	return eng
}

func params(tenant, user, month string) map[string]string {
	return map[string]string{"tenant": tenant, "user": user, "month": month}
}

// 验收边界：规则"VIP生日送积分"缓存了用户 U 的结果；当 U 的会员等级
// 变更时，仅 U 的积分规则缓存失效，同一租户下其他用户的缓存保持热态。
func TestAcceptance_PreciseInvalidation(t *testing.T) {
	eng := newEngineWithRule()

	const users = 10000
	for i := 1; i <= users; i++ {
		uid := fmt.Sprintf("U%d", i)
		eng.SetDimension(DimMember, MemberKey("t1", uid),
			map[string]string{"level": "VIP", "birthday": "1990-10-08"})
	}
	for i := 1; i <= users; i++ {
		uid := fmt.Sprintf("U%d", i)
		if _, err := eng.Evaluate(RuleVIPBirthdayPoints, params("t1", uid, "10")); err != nil {
			t.Fatalf("evaluate %s: %v", uid, err)
		}
	}
	if _, _, size := eng.Stats(); size != users {
		t.Fatalf("warmup: cache size = %d, want %d", size, users)
	}

	// U1 会员等级变更：精准定向剔除。
	evicted := eng.SetDimension(DimMember, MemberKey("t1", "U1"),
		map[string]string{"level": "SVIP"})
	if evicted != 1 {
		t.Fatalf("evicted = %d, want exactly 1", evicted)
	}
	if _, _, size := eng.Stats(); size != users-1 {
		t.Fatalf("after eviction: cache size = %d, want %d", size, users-1)
	}

	// U1 已冷，其余 9999 个用户全部保持热态。
	if eng.Cached(RuleVIPBirthdayPoints, params("t1", "U1", "10")) {
		t.Fatal("U1 cache should be invalidated")
	}
	for i := 2; i <= users; i++ {
		uid := fmt.Sprintf("U%d", i)
		if !eng.Cached(RuleVIPBirthdayPoints, params("t1", uid, "10")) {
			t.Fatalf("%s cache should stay hot", uid)
		}
	}

	// 失效传播：U1 重新计算得到新等级对应的结果，其余用户零重算。
	got, err := eng.Evaluate(RuleVIPBirthdayPoints, params("t1", "U1", "10"))
	if err != nil {
		t.Fatal(err)
	}
	if got != 200 {
		t.Fatalf("U1 after level change = %v, want 200 (SVIP)", got)
	}
	hits, misses, _ := eng.Stats()
	if misses != users+1 {
		t.Fatalf("misses = %d, want %d (warmup %d + U1 recompute 1)", misses, users+1, users)
	}
	_ = hits
}

func TestRuleResult(t *testing.T) {
	eng := newEngineWithRule()
	eng.SetDimension(DimMember, MemberKey("t1", "vip"),
		map[string]string{"level": "VIP", "birthday": "1990-10-08"})
	eng.SetDimension(DimMember, MemberKey("t1", "svip"),
		map[string]string{"level": "SVIP", "birthday": "1990-10-08"})
	eng.SetDimension(DimMember, MemberKey("t1", "normal"),
		map[string]string{"level": "NORMAL", "birthday": "1990-10-08"})

	cases := []struct {
		user, month string
		want        int
	}{
		{"vip", "10", 100},
		{"svip", "10", 200},
		{"normal", "10", 0},
		{"vip", "11", 0},   // 非生日月
		{"ghost", "10", 0}, // 维度数据缺失
	}
	for _, c := range cases {
		got, err := eng.Evaluate(RuleVIPBirthdayPoints, params("t1", c.user, c.month))
		if err != nil {
			t.Fatalf("%s/%s: %v", c.user, c.month, err)
		}
		if got != c.want {
			t.Fatalf("%s month=%s = %v, want %d", c.user, c.month, got, c.want)
		}
	}
}

// 反向索引随缓存剔除同步清理；未依赖被更新键的缓存不受影响。
func TestReverseIndexCleanupAndIsolation(t *testing.T) {
	eng := newEngineWithRule()
	eng.SetDimension(DimMember, MemberKey("t1", "U1"),
		map[string]string{"level": "VIP", "birthday": "1990-10-08"})
	eng.SetDimension(DimMember, MemberKey("t1", "U2"),
		map[string]string{"level": "VIP", "birthday": "1990-10-08"})

	if _, err := eng.Evaluate(RuleVIPBirthdayPoints, params("t1", "U1", "10")); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Evaluate(RuleVIPBirthdayPoints, params("t1", "U2", "10")); err != nil {
		t.Fatal(err)
	}

	// 更新 U1 后，反向索引中不应再残留指向已剔除缓存项的引用。
	eng.SetDimension(DimMember, MemberKey("t1", "U1"), map[string]string{"level": "NORMAL"})
	if n := len(eng.index[DimRef{Table: DimMember, Key: MemberKey("t1", "U1")}]); n != 0 {
		t.Fatalf("stale reverse index entries for U1: %d", n)
	}
	if _, ok := eng.deps[cacheKey(RuleVIPBirthdayPoints, params("t1", "U1", "10"))]; ok {
		t.Fatal("stale forward deps for evicted entry")
	}

	// 更新与缓存无关的维度键：零失效。
	if evicted := eng.SetDimension(DimMember, MemberKey("t1", "nobody"),
		map[string]string{"level": "VIP"}); evicted != 0 {
		t.Fatalf("unrelated update evicted %d entries, want 0", evicted)
	}
	if !eng.Cached(RuleVIPBirthdayPoints, params("t1", "U2", "10")) {
		t.Fatal("U2 cache must stay hot after unrelated update")
	}
}

// 依赖同一维度键的多条规则缓存一并失效；不同租户的同名用户互不影响。
func TestSharedKeyAndTenantIsolation(t *testing.T) {
	eng := newEngineWithRule()
	eng.RegisterRule("is_vip", func(tx *ReadTx, p map[string]string) (interface{}, error) {
		level, _ := tx.Get(DimMember, MemberKey(p["tenant"], p["user"]), "level")
		return level == "VIP" || level == "SVIP", nil
	})
	eng.SetDimension(DimMember, MemberKey("t1", "U1"),
		map[string]string{"level": "VIP", "birthday": "1990-10-08"})
	eng.SetDimension(DimMember, MemberKey("t2", "U1"),
		map[string]string{"level": "VIP", "birthday": "1990-10-08"})

	eng.Evaluate(RuleVIPBirthdayPoints, params("t1", "U1", "10"))
	eng.Evaluate("is_vip", params("t1", "U1", "10"))
	eng.Evaluate(RuleVIPBirthdayPoints, params("t2", "U1", "10"))

	// t1/U1 变更：t1 下两条规则缓存失效，t2/U1 保持热态。
	evicted := eng.SetDimension(DimMember, MemberKey("t1", "U1"),
		map[string]string{"level": "NORMAL"})
	if evicted != 2 {
		t.Fatalf("evicted = %d, want 2 (both rules on t1/U1)", evicted)
	}
	if !eng.Cached(RuleVIPBirthdayPoints, params("t2", "U1", "10")) {
		t.Fatal("tenant t2 cache must stay hot")
	}
}

func TestConcurrentEvaluateAndInvalidate(t *testing.T) {
	eng := newEngineWithRule()
	for i := 0; i < 100; i++ {
		uid := fmt.Sprintf("U%d", i)
		eng.SetDimension(DimMember, MemberKey("t1", uid),
			map[string]string{"level": "VIP", "birthday": "1990-10-08"})
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			uid := fmt.Sprintf("U%d", i)
			eng.Evaluate(RuleVIPBirthdayPoints, params("t1", uid, "10"))
		}(i)
		go func(i int) {
			defer wg.Done()
			uid := fmt.Sprintf("U%d", i)
			eng.SetDimension(DimMember, MemberKey("t1", uid),
				map[string]string{"level": "SVIP"})
		}(i)
	}
	wg.Wait()

	// 所有用户最终可算出一致的新结果。
	for i := 0; i < 100; i++ {
		uid := fmt.Sprintf("U%d", i)
		got, err := eng.Evaluate(RuleVIPBirthdayPoints, params("t1", uid, "10"))
		if err != nil {
			t.Fatal(err)
		}
		if got != 200 {
			t.Fatalf("%s = %v, want 200 after SVIP update", uid, got)
		}
	}
}
