# Acyclic Tree CRDT — 目录树移动/重命名协调器 (TypeScript)

多节点分布式虚拟目录树的 CRDT 实现：协调并发的 `move` / `rename` 操作，
保证合并后的目录树**永远无环、无孤立节点**，且所有副本**确定性收敛**到同一棵树。

## 核心机制

- **日志时间戳**：每个操作携带 `(lamport, replicaId, seq)` 混合逻辑时间戳，
  构成所有操作的**确定性全序**（Lamport 提供因果序，replicaId + seq 打破并发平局）。
- **父节点因果追溯**：归约（reduction）时对每个 `move` 从目标父节点向上遍历祖先链；
  若遇到被移动节点本身，说明该操作会闭合成环。
- **自动仲裁断环**：所有副本按同一全序重放同一操作日志；会成环的"后发生"操作
  被确定性地撤销（记录于 `droppedOps()`），先发生的操作胜出，目录不丢失。
- **收敛性**：合并 = 操作日志的并集（幂等、交换、结合），状态是日志的纯函数，
  因此所有副本必然收敛到同一棵无环森林。

## 运行

需要 Node.js ≥ 22.18（内置 TypeScript 类型擦除，无需编译）。

```sh
npm test        # Node.js 内置测试运行器 (node:test)
npm run demo    # 运行验收场景演示
```

## 验收场景

节点1 移动 `/dirA` 到 `/dirB` 下，节点2 并发移动 `/dirB` 到 `/dirA` 下。
合并后检测到拓扑依赖环，依据确定性全序撤销后发生的操作：

```
== node1 / node2 after merge (完全一致) ==
/  [root]
  dirB
    dirA
arbitration: reverted op from node2 ... cycle: ... would orphan a ring
```

## API 摘要

```ts
import { AcyclicTreeCrdt } from "./src/tree.ts";

const r = new AcyclicTreeCrdt("node1");
const dirA = r.create("dirA");        // 在 root 下创建目录
const dirB = r.create("dirB");
r.move(dirA, dirB);                   // 移动
r.rename(dirA, "docs");               // 重命名 (LWW)
r.merge(otherReplica);                // 合并（幂等/交换/结合）
r.pathOf(dirA);                       // "/dirB/docs"
r.state();                            // 物化树状态
r.droppedOps();                       // 被仲裁撤销的操作及原因
r.allReachable();                     // 无环/无孤立校验
```

## 文件结构

- `src/types.ts` — 操作类型、时间戳与确定性全序比较器
- `src/tree.ts` — `AcyclicTreeCrdt`：操作日志、合并、归约与断环仲裁
- `test/tree.test.ts` — 8 个 `node:test` 用例（含验收场景、三方成环、LWW 重命名、合并幂等交换性）
- `examples/demo.ts` — 可运行的验收场景演示
