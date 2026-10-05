# wait-graph-detector

跨分区等待图（wait-for graph）死锁诊断器。各分区的本地等待图各自无环时，
合并后的全局图仍可能存在跨分区环——检测始终运行在汇聚后的全局视图上。

## 设计要点

- **带版本的边汇聚**：每条等待边以 `(partition, from, to)` 为键，按分区本地
  单调版本号做 last-write-wins 合并（`src/versioned-store.ts`）。
- **乱序安全**：`remove` 留下带版本的墓碑（tombstone），延迟到达的旧
  `upsert` 无法复活已删除的边；同理乱序的 `txn-begin` 无法复活已结束的事务。
  同版本冲突时删除优先（确定性），重复事件被丢弃并计入 `stats().dropped*`。
- **最短环输出**：`detect()` 对全局图逐边做 BFS，返回最短环的节点序列、
  每条边的分区与版本号，以及用于过期校验的 `fingerprint`。
- **撤销建议只针对存活事务**：`suggestVictim()` 只在 `isAlive()` 的环成员中
  选择受害者。
- **过期快照不误杀**：`abort()` 执行前重新校验快照——环中每条边必须仍以
  原版本存在，且受害者仍存活；任一条件不满足则拒绝（`stale-snapshot` /
  `victim-not-live`），绝不基于过期信息杀事务。

## 使用

```ts
import { WaitGraph } from "./src/index.ts";

const g = new WaitGraph();
g.applyAll([
  { kind: "txn-begin", partition: "p1", txn: "T1", version: 1 },
  { kind: "txn-begin", partition: "p1", txn: "T2", version: 2 },
  { kind: "edge-upsert", partition: "p1", from: "T1", to: "T2", version: 3 },
  { kind: "edge-upsert", partition: "p2", from: "T2", to: "T1", version: 3 },
]);

const report = g.detect();          // 最短环 + 边版本
if (report) {
  const victim = g.suggestVictim(report);   // 只选仍存活的事务
  if (victim) g.abort(report, victim);      // 快照过期时安全地拒绝
}
```

## 运行测试

需要 Node ≥ 23.6（原生类型擦除），或先用 tsc 编译：

```sh
npm test            # node --test test/detector.test.ts
npm run typecheck   # 需要安装 typescript（npm i）
```

## 事件模型

| 事件 | 含义 |
| --- | --- |
| `edge-upsert` / `edge-remove` | 分区上报等待边出现/消失，带分区单调版本 |
| `txn-begin` / `txn-end` | 分区上报事务存活状态，带分区单调版本 |

约束：事务 id 全局唯一且不复用；版本号在每个分区内单调递增。
