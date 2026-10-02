// 运行样例：VIP生日送积分规则的缓存与精准失效演示。
package main

import (
	"fmt"

	"rulecache"
)

func main() {
	eng := rulecache.New()
	eng.RegisterRule(rulecache.RuleVIPBirthdayPoints, rulecache.VIPBirthdayPoints)

	// 1. 装载维度数据：租户 t1 下 10000 个用户，U1 是 10 月生日的 VIP。
	const users = 10000
	for i := 1; i <= users; i++ {
		uid := fmt.Sprintf("U%d", i)
		level, birthday := "NORMAL", "1990-03-15"
		if i == 1 {
			level, birthday = "VIP", "1990-10-08"
		}
		eng.SetDimension(rulecache.DimMember, rulecache.MemberKey("t1", uid),
			map[string]string{"level": level, "birthday": birthday})
	}

	// 2. 高频查询：为全部用户计算 10 月积分，结果全部进入缓存。
	for i := 1; i <= users; i++ {
		uid := fmt.Sprintf("U%d", i)
		eng.Evaluate(rulecache.RuleVIPBirthdayPoints,
			map[string]string{"tenant": "t1", "user": uid, "month": "10"})
	}
	hits, misses, size := eng.Stats()
	fmt.Printf("预热完成: 缓存项=%d 命中=%d 未命中=%d\n", size, hits, misses)

	// 3. 再次查询 U1：命中缓存。
	p1 := map[string]string{"tenant": "t1", "user": "U1", "month": "10"}
	r, _ := eng.Evaluate(rulecache.RuleVIPBirthdayPoints, p1)
	fmt.Printf("U1 变更前结果(缓存命中): %v 分\n", r)

	// 4. U1 的会员等级变更 VIP -> SVIP：仅 U1 的缓存被精准剔除。
	evicted := eng.SetDimension(rulecache.DimMember, rulecache.MemberKey("t1", "U1"),
		map[string]string{"level": "SVIP"})
	hits, misses, size = eng.Stats()
	fmt.Printf("U1 等级变更: 剔除缓存项=%d 剩余缓存=%d (其余 %d 用户零失效)\n",
		evicted, size, users-1)

	// 5. 失效传播后：U1 重新计算得到新结果，其他用户仍命中热缓存。
	r, _ = eng.Evaluate(rulecache.RuleVIPBirthdayPoints, p1)
	fmt.Printf("U1 变更后结果(重新计算): %v 分\n", r)
	r, _ = eng.Evaluate(rulecache.RuleVIPBirthdayPoints,
		map[string]string{"tenant": "t1", "user": "U2", "month": "10"})
	hits, misses, _ = eng.Stats()
	fmt.Printf("U2 结果(仍命中热缓存): %v 分; 全局命中=%d 未命中=%d\n", r, hits, misses)
}
