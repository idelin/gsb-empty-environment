# migplan — 迁移脚本规划器

按读写对象与前置状态把迁移步骤调度成若干阶段（phase），合并不冲突的操作，
聚拢需要停写的步骤以最小化停写窗口数，并标出不可逆边界。

## 规则

- **步数上限**：最多 20 步，超出报错。
- **前置状态**：`requires` 声明依赖；必须指向已知步骤且无环。
- **生命周期顺序**：同一 `(table, column)` 上自动注入并校验
  `add_column → backfill → add_constraint`（以及 `add_column → create_index`）。
  显式依赖若与此相反（如 backfill 依赖 constraint）直接报错；
  缺失的环节由规划器自动补上，保证不会错序。
- **冲突合并**：写-写、读-写作用于同一对象（表级对象覆盖其所有列）的步骤
  不会进入同一阶段；互不冲突的步骤合并并行。
- **最少停写窗口**：`needs_write_stop` 的步骤在其依赖允许的范围内尽量后移，
  用区间覆盖（interval stabbing）贪心聚类到最少阶段；依赖或冲突迫使分开时
  会产生多个窗口（仍为合法调度）。
- **不可逆边界**：`rollback` 为空或属破坏性操作（`drop_column`/`drop_table`）
  的步骤不可逆；计划中第一个不可逆步骤所在阶段即"不可回退点"，
  该阶段之后无法完整回滚。

## 使用

库：

```go
plan, err := planner.Schedule(steps) // steps []planner.Step
```

CLI（JSON 从 stdin 或文件读入，计划输出到 stdout）：

```sh
go run ./cmd/migplan example.json
```

步骤未显式给出 `reads`/`writes` 时，按 `kind` 推导默认读写集合
（如 `backfill` 读整表、写目标列；`add_constraint` 读写整表）。

## 测试

```sh
go test ./...
```
