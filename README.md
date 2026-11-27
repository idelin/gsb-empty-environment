# IaC 依赖规划与执行图引擎

`prescreen.local/ps-2bc7f50ad6` 是一个可直接导入的 Go 包（包名 `iac`），把声明式资源、远端漂移和生命周期规则编译为经过安全校验的执行 DAG。

## 快速开始

```go
package main

import (
	"fmt"

	"prescreen.local/ps-2bc7f50ad6"
)

func main() {
	resources := []iac.Resource{
		{Address: "net", Change: iac.ProposedChange{Action: iac.Replace}},
		{Address: "db", DependsOn: []string{"net"}, Change: iac.ProposedChange{Action: iac.Replace}},
		{
			Address:   "app",
			DependsOn: []string{"db"},
			Change:    iac.ProposedChange{Action: iac.Replace},
			Lifecycle: iac.Lifecycle{CreateBeforeDestroy: true},
		},
	}

	plan, err := iac.BuildPlan(resources)
	if err != nil {
		panic(err)
	}

	fmt.Print(plan)
	// app/db/net 均为 CreateThenDestroy；执行波次为
	// create:net -> create:db -> create:app -> destroy:app -> destroy:db -> destroy:net

	result := plan.Execute(func(n *iac.Node) error {
		fmt.Println("running", n.ID)
		return nil
	})
	fmt.Println(result.Succeeded, result.Failed, result.Skipped)
}
```

## 规划模型

`Resource` 的主要字段：

- `Address`：唯一资源地址。
- `DependsOn`：上游依赖；`A` 依赖 `B` 表示创建时 `B` 先于 `A`，销毁时 `A` 先于 `B`。
- `Change` / `Action` / `ForceNew`：显式变更。`UpdateInPlace + ForceNew` 会升级为 `Replace`。
- `Remote` / `Desired` / `ForceNewAttributes`：未显式指定动作时自动做漂移检测，区分 `Create`、`NoOp`、`UpdateInPlace` 和 `Replace`。
- `Lifecycle.CreateBeforeDestroy`：替换时先建后删。
- `Lifecycle.ForbidCBD`：禁止新老实例并存；CBD 传播到此资源时报 `CBD_CONFLICT`。
- `Lifecycle.PreventDestroy`：只要最终计划销毁该资源就阻断整份规划。
- `ReplaceTriggeredBy`：被引用资源发生更新或替换时，本资源级联升级为 `Replace`。

`BuildPlan(resources, opts...)` 返回 `*Plan`，其中 `Actions` 是资源到最终动作的映射，`Resources` 包含原因、漂移字段和触发链，`Graph` 是拆分后的动作图，`Waves` 是最大并发拓扑波次。

## 图与调度

最终动作只有：

`NoOp`、`Create`、`UpdateInPlace`、`Destroy`、`CreateThenDestroy`、`DestroyThenCreate`。

替换资源会拆成两个节点，ID 形如 `create:<address>` 与 `destroy:<address>`；就地更新节点为 `update:<address>`。依赖接线包括：

- 新对象沿依赖方向创建：`create/update(B) -> create(A)`。
- 旧对象沿依赖反方向销毁：`destroy(A) -> destroy(B)`。
- CBD 下游新对象必须先建好：`create/update(A) -> destroy(B)`。
- 替换顺序由 CBD 决定：`create(A) -> destroy(A)` 或 `destroy(A) -> create(A)`。

`Graph.FindMinimalCycle()` 返回最短有向环闭合路径；`Graph.TopologicalWaves()` 输出最大并发波次；`Graph.Validate()` 同时检查结构和无环性。

## Target 闭包

```go
// apply -target=X：X + 全部上游依赖
plan, err := iac.BuildPlan(resources, iac.PlanOptions{Targets: []string{"X"}})

// destroy -target=X：X + 全部下游依赖，执行时下游先销毁
plan, err = iac.BuildPlan(resources, iac.PlanOptions{Targets: []string{"X"}, Destroy: true})
```

也可直接调用 `UpstreamClosure` / `DownstreamClosure` 获取闭包地址。

## 安全错误

所有规划安全错误均为 `*iac.PlanError`，可用 `iac.HasErrorCode(err, code)` 判断：

- `CBD_CONFLICT`：`Chain` / `Chains` 包含完整冲突依赖路径。
- `PREVENT_DESTROY`：`Violations` 列出资源、动作和触发链。
- `DEPENDENCY_CYCLE`：`Cycle` 是输入依赖图的极小环。
- `EXECUTION_CYCLE`：`Cycle` 是拆分替换节点后动作图的极小环。
- `UNKNOWN_RESOURCE` / `INVALID_ACTION` / `INVALID_GRAPH`：输入错误。

## 局部失败隔离

`Plan.Execute` / `Graph.Execute` 在每个波次内并发执行就绪节点。某个节点失败时，只有它的传递下游被标记为 `Skipped`，同波次和后续波次中的独立分支继续执行。`ExecuteWithFailures(map[string]error{...})` 提供按节点 ID 或资源地址注入失败的确定性模拟。

```go
result := plan.ExecuteWithFailures(map[string]error{
	"create:db": errors.New("quota exceeded"),
})
// result.Failed / result.Skipped / result.BlockedBy 给出最小影响范围
```

## 验证

```bash
go test ./...
go test -race ./...
```

测试覆盖多层 CBD 向上传播、`forbid_cbd` 冲突链、`replace_triggered_by` 触发 `prevent_destroy`、apply/destroy 正反 target 闭包、替换拆节点环检测，以及单分支失败的局部阻断。
