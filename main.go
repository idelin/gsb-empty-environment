package main

import "fmt"

// 运行样例：验收边界场景
//
//	知悉 0  : 管理员直接授予 A 角色 R，业务区间 [0, 100)
//	知悉 10 : A 委托 R 给 B，业务区间 [10, 50)
//	知悉 20 : B 在业务 20 又委托 R 给 C，业务区间 [20, 60)
//	知悉 30 : A 提前收回对 B 的委托，自业务 25 起失效
//	知悉 35 : 查询 C 在业务 28 的权限 —— 应判定已失效
func main() {
	r := NewResolver()
	r.DefineRole("R", "doc:read", "doc:write")

	r.GrantRole("A", "R", Interval{0, 100}, 0)

	ab, err := r.Delegate("A", "B", "R", Interval{10, 50}, 10)
	must(err)
	fmt.Printf("[kt=10] A delegates R to B over [10, 50)  -> grant #%d\n", ab)

	bc, err := r.Delegate("B", "C", "R", Interval{20, 60}, 20)
	must(err)
	fmt.Printf("[kt=20] B delegates R to C over [20, 60)  -> grant #%d\n", bc)

	must(r.Revoke(ab, 25, 30))
	fmt.Printf("[kt=30] A revokes grant #%d, effective at business time 25\n", ab)

	fmt.Println()
	query := func(user string, bt, kt int64) {
		perms := r.EffectivePermissions(user, bt, kt)
		fmt.Printf("query user=%s business=%d knowledge=%d -> holds R: %-5v permissions: %v\n",
			user, bt, kt, r.HoldsRole(user, "R", bt, kt), perms)
	}

	query("C", 22, 35) // 收回生效前：有效
	query("C", 28, 29) // 收回尚未知悉：有效
	query("C", 28, 35) // 验收点：收回已知悉且已生效 -> 失效
	query("C", 55, 35) // 上游委托 [10,50) 已过期 -> 失效
	query("B", 28, 35) // B 自身在业务 28 同样已失效
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
