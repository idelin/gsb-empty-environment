# 跨区域防火墙更新规划

`taskpkg` 供 Go 策略控制宿主调用。每个区域拥有一组可变过滤边，一步只允许该区域原子地把整组边替换为目标版本。规划器必须让每一个已发布步骤都满足：

- 所有指定攻击入口都无法到达任一秘密节点。
- 每条受认证管理路径仍然可达。
- 任一配置中活跃的可变过滤边不超过 12 条。

## 用法

```go
package main

import (
	"fmt"
	"log"

	"example.org/domainreissue/q030"
)

func main() {
	req := taskpkg.Request{
		Regions: []taskpkg.Region{
			{
				ID:             "entry",
				CurrentVersion: "open",
				TargetVersion:  "closed",
				Versions: []taskpkg.Version{
					{Name: "open", Edges: []taskpkg.Edge{{From: "attacker", To: "conduit"}}},
					{Name: "closed"},
				},
				CanConfirm: true,
			},
			{
				ID:             "core",
				CurrentVersion: "closed",
				TargetVersion:  "open",
				Versions: []taskpkg.Version{
					{Name: "closed"},
					{Name: "open", Edges: []taskpkg.Edge{{From: "conduit", To: "secret"}}},
				},
				CanConfirm: true,
			},
		},
		FixedEdges:      []taskpkg.Edge{{From: "admin", To: "secret"}},
		AttackEntries:   []string{"attacker"},
		Secrets:         []string{"secret"},
		ManagementPaths: []taskpkg.ManagementPath{{From: "admin", To: "secret"}},
	}

	plan, err := taskpkg.Plan(req)
	if err != nil {
		log.Fatal(err) // ErrNoSafeOrder: 不发布任何首步
	}
	fmt.Println(plan.Steps)
}
```

默认假设回执可能乱序、区域可能失联，因此会遍历所有已发布更新的安装子集（最多 `2^12` 个配置）。无法确认安装的区域还会同时核验旧边集和新边集。只有存在完整安全顺序时才返回 `*UpdatePlan`；否则返回 `nil` 与 `ErrNoSafeOrder`，调用方不会拿到可发布的局部计划。

若控制器确实串行等待回执且保证区域不失联，可设置 `AssumeOrderedReceipts` 和 `AssumeNoRegionLoss` 放宽核验。`ValidatePlan` 使用同一组假设计算对已有计划做复核。

运行测试：

```sh
go test ./...
```
