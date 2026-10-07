# workflow-versioned

基于事件溯源（event sourcing）的 TypeScript 工作流引擎，支持图版本升级与实例迁移。
零依赖，要求 Node.js ≥ 22.6（利用原生类型擦除直接运行 `.ts`）。

## 核心概念

- **版本化图（`GraphVersion`）**：状态、初始状态与迁移表（事件 → 源状态集 → 目标状态）。
  非首个版本必须声明 `stateMapping`（旧状态 → 新状态），映射可以是多对一（状态合并）。
- **事件日志（`InstanceEvent[]`）**：每个实例一条只追加日志，`created` / `transition` /
  `migrated` 三类事件共享同一单调递增的 `seq`，迁移与新事件在同一序列中原子排序。
- **命令（`Command`）**：携带发出时所见到的 `graphVersion`，用于拒绝旧版本的迟到命令。

## 用法

```ts
import { WorkflowEngine } from "workflow-versioned";

const engine = new WorkflowEngine();
engine.registerGraph(graphV1);          // 首个版本，无 stateMapping
engine.registerGraph(graphV2);          // 必须声明从 v1 的 stateMapping

engine.start("order-1", 1);             // 在旧版本上创建实例
engine.dispatch("order-1", { event: "pay", graphVersion: 1 });

const persisted = engine.snapshot("order-1").log;
engine.restore("order-2", persisted);   // 从旧版本日志恢复实例（重放校验历史）

engine.migrateBatch(["order-1", "order-2"], 2);  // 批量升级，全有或全无
engine.dispatch("order-1", { event: "refund", graphVersion: 2 });
```

## 语义保证

- **从旧版本恢复**：`restore(id, log)` 通过完整历史重放校验后重新水合实例，
  篡改或损坏的历史抛出 `CorruptHistoryError`。
- **状态合并保留历史**：多个旧状态映射到同一新状态时，历史事件原样保留，
  `migrated` 事件记录完整映射快照，审计可追溯每个旧状态。
- **整批原子性**：`migrateBatch` 先验证全部实例，任一实例的活跃状态未映射
  （或版本不符）则抛出 `BatchMigrationError`，无任何实例被迁移。
- **迟到命令拒绝**：命令携带的 `graphVersion` 与实例当前版本不一致时
  抛出 `StaleCommandError`。
- **历史重放核对**：`engine.audit(id)` 重放全部历史，校验每个迁移在其图版本下
  合法，并对每次迁移给出升级前后的可执行动作（`actionsBefore` / `actionsAfter`）
  及合并来源（`mergedFrom`）。

## 测试

```sh
npm test
```
