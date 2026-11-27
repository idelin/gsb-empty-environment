# `systemd` 单元依赖事务求解器

一个无外部依赖、可导入的 Go 包，用确定性内存算法展开 systemd 风格的单元事务，仲裁冲突并生成可执行的启动/停止排期。

```go
import "prescreen.local/ps-19c50a7725"

graph := systemd.NewGraph(
	systemd.Unit{Name: "web.service", Requires: []string{"db.service"}},
	systemd.Unit{Name: "db.service"},
)

plan, err := graph.Start("web.service", systemd.ModeReplace)
```

包名是 `systemd`。模块路径为 `prescreen.local/ps-19c50a7725`，也可使用别名导入：

```go
import units "prescreen.local/ps-19c50a7725"
```

## 快速接口

- `NewGraph(units ...Unit) *Graph`、`NewGraphFromUnits([]Unit)`、`NewGraphPtr(...*Unit)`。
- `Graph.Start/Stop/Restart/Isolate(unit, mode...)`。
- `Graph.Solve(Request)`、`Graph.SolveAll(...Request)`。
- 包级 `Solve(graph, request)`、`SolveAll(graph, requests...)`，以及 `Start/Stop/Restart/Isolate` 请求构造器。
- `Solver{Graph: graph}` 可复用；`NewSolver(graph)` 和 `NewSolverFromUnits(units...)` 是便捷构造器。

`Request.Action` 和 `Request.Type` 都是操作别名，`Action` 优先。空 `Mode` 默认为 `ModeReplace`。

## 核心语义

### 需求与顺序正交

`Requires`、`Wants`、`BindsTo` 会沿依赖方向拉入 `start` 作业，`Requisite` 只检查当前快照。它们都不会自动生成 `Before`/`After` 边。

```go
systemd.Unit{
	Name:     "app.service",
	Requires: []string{"db.service"},
	After:    []string{"db.service"}, // 顺序必须显式声明
}
```

### `Requisite`

- 目标的当前状态必须为活跃。
- 未活跃时整个事务返回 `ErrRequisiteInactive`，不会生成目标的 `start` 作业。
- 目标已活跃时，只在 `Plan.Dependencies` 中记录关系，不生成冗余作业。

### 作业合并与冲突

同一单元最多保留一个作业。已实现的主要合并规则：

- `start + start -> start`
- `start + restart -> restart`
- `restart + stop -> stop`
- `stop + restart -> stop`
- `stop + stop -> stop`

`start` 与 `stop` 冲突时：

- `ModeFail`：立即拒绝整个事务，返回 `ErrTransactionConflict`。
- `ModeReplace`：锚定作业不可删除；两个锚定或两个核心需求冲突仍失败；非锚定、非核心的一方可以被裁剪。
- `ModeReplaceIrreversibly`：采用相同仲裁规则，并把最终保留的作业标记为 `Irreversible`。
- `Requires`/`BindsTo` 拉入的作业标记为 `Core`；由 `Wants` 拉入的作业不是核心。

`Plan.RemovedJobs` 记录被替换或裁剪的作业。

### 停止与 `PartOf`

- 显式停止一个被 `Requires`/`Requisite`/`BindsTo` 依赖的单元，会向依赖它的活跃单元传播 `stop`。
- `a.PartOf = ["b.service"]` 表示停止或重启 `b` 会向 `a` 传播。
- `Wants` 不会传播停止。

### `isolate`

`Graph.Isolate(target, mode)`：

1. 计算目标的静态可达依赖闭包（`Requires`/`Requisite`/`Wants`/`BindsTo`）。
2. 保护目标、闭包内单元和所有 `IgnoreOnIsolate` 单元。
3. 给其余所有活跃单元加入 `stop` 作业。
4. 目标未活跃时加入锚定 `start` 作业。

### 顺序 DAG 与反向停止顺序

若声明 `A After B`：

- 启动阶段：`B -> A`
- 停止阶段：`A -> B`

若声明 `A Before B`，启动阶段为 `A -> B`，停止阶段反转为 `B -> A`。

`Plan` 提供：

- `Edges`：最终作业级 DAG 边。
- `StartEdges` / `StopEdges`：分阶段顺序边。
- `Order`：全部保留作业的确定性拓扑序。
- `StartOrder` / `StopOrder`：启动与停止阶段的拓扑序。
- `Steps`：可执行步骤；`restart` 展开为一条 `stop` 步骤和一条 `start` 步骤。

所有拓扑序在无先后约束时按单元名排序，仅作为确定性 tie-break，不表示隐式依赖。

## 确定性破环

破环只删除 `Before`/`After` 排序边，不会删除作业：

1. 在最终作业图中寻找长度最短、字典序最小的有向简单环。
2. 在环内按 `(From, To, Phase, Relation, Source)` 字典序选择最大的一条非关键排序边删除。
3. 重复直到图无环；每个被破除的环写入 `Plan.Cycles`，所有被删边写入 `Plan.CutEdges`。
4. 最后按字典序最小拓扑序输出 `Order`/`StartOrder`/`StopOrder`。

整个过程有确定性的遍历顺序和有限次删边，不会依赖 map 迭代顺序，也不会丢弃有效作业。

## 状态快照

`Unit.Active` 是主要活跃标志；也可设置 `Unit.State` 为 `StateActive` 或 `StateReloading`。`Unit.IsActive()` 统一判定。`Graph.SetActive(name, bool)` 可更新快照。

## 错误

可用 `errors.Is` 判断：

- `ErrInvalidRequest`
- `ErrMissingUnit`
- `ErrRequisiteInactive`
- `ErrTransactionConflict`
- `ErrUnsatisfiedConflict`

`*TransactionError` 额外提供 `Kind`、`Unit`、`Related` 和 `Message`。

## 验证

```sh
go test ./...
go vet ./...
```

测试覆盖需求/顺序正交性、`Requisite` 不自动拉起、`fail`/`replace` 锚定保护、冲突停止、`isolate`/`IgnoreOnIsolate`、停止顺序反转、多环确定性破除、作业合并及 `PartOf` 传播。
