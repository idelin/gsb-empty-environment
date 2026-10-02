package main

import (
	"fmt"
	"time"

	"rulecache"
)

func main() {
	store := rulecache.NewDimensionStore()
	engine := rulecache.NewEngine(store, rulecache.NewCache())
	rule := rulecache.VIPBirthdayBonusRule{}
	tenant := "tenant-1"
	month := time.Now().Format("01")

	store.Set(rulecache.DimKey(tenant+":config:campaign"), map[string]string{"birthday_bonus_enabled": "true"})

	// 模拟同一租户下 10000 个用户
	const n = 10000
	for i := 0; i < n; i++ {
		u := fmt.Sprintf("U%d", i)
		level := "NORMAL"
		if i%10 == 0 {
			level = "VIP"
		}
		store.Set(rulecache.DimKey(tenant+":user:"+u+":member"), map[string]string{"level": level})
		store.Set(rulecache.DimKey(tenant+":user:"+u+":profile"), map[string]string{"birthday": "1990-" + month + "-15"})
	}

	// 高频查询：全部用户各查两次，第二次全部命中缓存
	for round := 0; round < 2; round++ {
		for i := 0; i < n; i++ {
			_, _ = engine.Evaluate(rule, tenant, fmt.Sprintf("U%d", i))
		}
	}
	s := engine.Stats()
	fmt.Printf("预热后: 缓存项=%d 命中=%d 未命中=%d 失效=%d\n", s.Size, s.Hits, s.Misses, s.Invalidations)

	// 验收场景：用户 U42 会员等级 NORMAL -> VIP
	before, _ := engine.Evaluate(rule, tenant, "U42")
	fmt.Printf("U42 升级前积分: %v\n", before)

	store.Set(rulecache.DimKey(tenant+":user:U42:member"), map[string]string{"level": "VIP"})

	s = engine.Stats()
	fmt.Printf("U42 等级变更后: 缓存项=%d 失效=%d (仅剔除 U42 的 1 条，其余 %d 条保持热态)\n", s.Size, s.Invalidations, s.Size)

	after, _ := engine.Evaluate(rule, tenant, "U42")
	fmt.Printf("U42 升级后积分: %v (重新计算)\n", after)

	// 抽查其他用户缓存仍为热态：再查一轮，全部命中
	for i := 0; i < n; i++ {
		_, _ = engine.Evaluate(rule, tenant, fmt.Sprintf("U%d", i))
	}
	s = engine.Stats()
	fmt.Printf("复查询后: 命中=%d 未命中=%d (未命中仅 U42 重算 1 次)\n", s.Hits, s.Misses)
}
