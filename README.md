Linux `systemd` 服务管理器的事务求解器有三条极易被直觉误导的核心语义：1）**需求依赖（`Requires`/`Requisite`/`Wants`/`BindsTo`/`PartOf`/`Conflicts`）与先后顺序（`Before`/`After`）完全正交**——`A Requires B` 只决定把 `start(B)` 拉入事务（且 `B` 失败时连带取消 `A`），绝不意味着 `A` 必须排在 `B` 之后启动，顺序只由显式 `Before`/`After` 决定！2）`Requisite` 要求目标单元**当前已经处于活跃态**，绝不可像 `Requires` 那样自动生成 `start` 作业去拉起它（目标未活跃时直接判定事务失败！）；3）当一次事务因传递依赖对同一单元同时衍生出 `start` 与 `stop` 冲突作业时，结果取决于事务模式：`fail` 直接回滚拒绝整事务；`replace` 仅当引发冲突的一方源自非锚定（non-anchor）传递依赖时才允许裁剪替换，若两个冲突作业均为锚定或不可消除的核心需求则仍须失败；`isolate` 会把所有未标记 `IgnoreOnIsolate` 且非目标可达依赖的活跃单元统一加入 `stop` 集合！此外，当事务最终作业图的 `Before`/`After` 边构成有向环时，必须按确定性破环策略识别极小环、删除非关键排序边并记录全部被剪断的环与边，绝不能陷入死循环或直接丢弃有效作业。为节点运维代理交付可导入的 Go 服务单元依赖事务求解与执行排期包。规则：1）支持完整单元依赖图（`Requires`/`Requisite`/`Wants`/`BindsTo`/`PartOf`/`Conflicts`/`Before`/`After`/`IgnoreOnIsolate`）与当前单元活跃状态快照；2）实现 `start`/`stop`/`restart`/`isolate` 请求在 `fail`/`replace`/`replace-irreversibly` 模式下的事务展开、作业合并（如 `start + start -> start`、`restart + stop -> stop`）、锚定保护与冲突仲裁；3）在保留事务作业集合上叠加顺序边，检测并确定性破除全部 `Before`/`After` 排序环，输出带先后依赖 DAG 的可执行作业计划（`start` 与 `stop` 遵循反向顺序约定：若 `A After B`，则启动时 `B -> A`，而停止时反向 `A -> B`！）。验收覆盖需求与顺序正交性、`Requisite` 不自动拉起失败、`Conflicts` 在 `fail` vs `replace` 模式下锚定保护差异、`isolate` 与 `IgnoreOnIsolate`、停止阶段 `Before`/`After` 顺序自动反转以及多环确定性破环报告。

## 交付物：`txn` 包

导入路径 `prescreen.local/ps-19c50a7725/txn`，纯标准库实现，无外部依赖。

- `txn.Graph` / `txn.Unit`：完整单元依赖图（含 `IgnoreOnIsolate`）。
- `txn.Snapshot`：当前活跃状态快照，`Requisite` 与 `Conflicts` 据此判定。
- `txn.Solve(g, snap, req) (*txn.Plan, error)`：唯一入口，失败即整体回滚（返回哨兵错误，可用 `errors.Is` 匹配 `ErrRequisiteNotActive` / `ErrConflict` / `ErrUnknownUnit` / `ErrInvalidRequest`）。
- `txn.Plan`：`Jobs`（保留作业，含 `Anchor`/`Irreducible` 标记）、`Edges`（DAG 顺序边，`Reversed` 标记停止反转）、`Layers`（可并行执行的拓扑分层）、`Cuts`（每个被剪断的极小环及其删除边）。

### 语义要点

- **正交性**：`Requires`/`Wants`/`BindsTo`/`Requisite` 只拉入作业，绝不产生顺序边；顺序仅来自 `Before`/`After`。
- **Requisite 门控**：目标未活跃直接 `ErrRequisiteNotActive`；活跃则生成内部 `JobVerifyActive` 空作业参与排序，绝不拉起 `start`。
- **冲突仲裁**：`fail` 立即失败；`replace` 仅允许替换非锚定且可消除（`Wants`/传播类）作业；`replace-irreversibly` 额外允许替换非锚定的硬需求作业；锚点在任何模式下都受保护。stop 侧在允许替换时始终胜出（与入队顺序无关）。
- **isolate**：保留锚点需求/传播闭包，对其余活跃单元统一生成 `stop`，`IgnoreOnIsolate` 单元跳过。
- **停止反转**：只要顺序边任一端点是 `stop` 作业，边方向即反转，使停机顺序与启动相反；`Reversed=true` 记录该翻转。
- **确定性破环**：Tarjan SCC + BFS 极小环，优先剪断不含锚点的非关键边，字典序兜底；只删边不删作业，全部剪切断点写入 `Plan.Cuts`，过程保证终止。

### 快速示例

```go
g := &txn.Graph{Units: map[string]*txn.Unit{
    "app.service": {Name: "app.service", Requires: []string{"db.service"}, After: []string{"db.service"}},
    "db.service":  {Name: "db.service"},
}}
plan, err := txn.Solve(g, txn.Snapshot{}, txn.Request{Unit: "app.service", Job: txn.JobStart, Mode: txn.ModeFail})
// plan.Layers => [[db.service] [app.service]]
```

### 测试

`go test ./...` 覆盖：需求/顺序正交性、`Requisite` 不拉起、`Conflicts` 的 `fail`/`replace`/`replace-irreversibly` 锚定保护差异、`isolate` + `IgnoreOnIsolate`、停止顺序反转、自环/多环/带弦 SCC 的确定性破环报告，以及 `start+start`、`restart+stop` 等合并规则。
