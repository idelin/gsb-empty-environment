// 运行样例：验收场景的双时态查询演示。
package main

import (
	"fmt"

	temporal "temporal-rbac"
)

func main() {
	r := temporal.NewResolver()
	r.SetRolePermissions("R", "doc:read", "doc:write")

	// 系统直接授予 A 角色 R。
	r.GrantRole(temporal.Grant{Grantor: "", Grantee: "A", Role: "R",
		ValidFrom: 0, ValidTo: temporal.Infinity, RecordedAt: 0})
	// A 委托 R 给 B，业务时间 [10, 50)。
	ab := r.GrantRole(temporal.Grant{Grantor: "A", Grantee: "B", Role: "R",
		ValidFrom: 10, ValidTo: 50, RecordedAt: 10})
	// B 在 20 委托 R 给 C，业务时间 [20, 60)。
	r.GrantRole(temporal.Grant{Grantor: "B", Grantee: "C", Role: "R",
		ValidFrom: 20, ValidTo: 60, RecordedAt: 20})
	// A 在知悉时间 30 撤回对 B 的委托，自业务时间 25 起失效。
	r.Revoke(ab, 25, 30)

	type q struct {
		user     string
		vt, tt   int64
		scenario string
	}
	queries := []q{
		{"C", 28, 25, "撤回未知悉（审计25<30），C 在业务28"},
		{"C", 28, 35, "验收边界：知悉35查业务28，应已失效"},
		{"C", 24, 35, "撤回自业务25生效，业务24仍有效"},
		{"C", 55, 25, "上游 A->B 业务50过期，C 在业务55失效"},
		{"B", 30, 35, "B 自身在业务30，已被撤回"},
		{"A", 30, 35, "A 为系统直授，不受影响"},
	}
	for _, query := range queries {
		perms := r.EffectivePermissions(query.user, query.vt, query.tt)
		if len(perms) == 0 {
			perms = []string{"<无>"}
		}
		fmt.Printf("用户%s 业务时间=%2d 审计时间=%2d -> 权限=%v  (%s)\n",
			query.user, query.vt, query.tt, perms, query.scenario)
	}
}
