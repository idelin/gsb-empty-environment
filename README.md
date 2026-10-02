# Dotted Version Vectors (DVV) 精确因果跟踪器

面向**无主复制（Leaderless）**场景的键级因果跟踪与去重优化实现。
每次写入被表示为一个**点状事件 `(NodeID, Counter)`** 与一个**前置向量（causal context）**，
从而精确区分「相同值的重复写入」与「不同值的并发覆盖」，消除传统版本向量的幽灵分支。

纯 TypeScript、零依赖，使用 Node.js 内置测试运行器（`node:test`）。

## 运行环境

- Node.js >= 22.6（利用内置 TypeScript 类型擦除直接运行 `.ts`，无需编译、无任何 npm 依赖）
- 仓库内 `.nvmrc` 指向 22.14.0；`nvm use` 后即可

```bash
npm test    # 运行全部 node:test 测试
npm run demo  # 运行无主复制 + 乱序广播演示
```

## 数据模型

```
Dot        = { node: NodeID, counter: number }        -- 全局唯一的写事件标识
VersionVector = Map<NodeID, number>                   -- 前置向量 / 因果上下文
Sibling    = { dot, value }                           -- 一个仍存活（并发）的值
DVVEntry   = { context: VersionVector, siblings: Sibling[] }
```

不变式：`context` 记录副本已见的全部事件（含 sibling 点自身的计数），
`siblings` 中保存两两并发、且值互不相同的写事件。

## 核心算法

### put(key, value, readContext)

1. `counter = max(context[node], readContext[node], 本节点现存 sibling 计数) + 1`，
   生成新点 `(node, counter)` —— 点状标识保证每次写入全局唯一、可幂等识别。
2. 丢弃被 `readContext` 覆盖的 sibling（客户端已读并因果 supersede）。
3. **同值去重**：幸存 sibling 的值与新值相同（结构深比较）时，将其点**折叠进 context**
   而非保留为分支 —— 重试风暴 / 空更新不会产生幽灵分支。
4. 不同值的并发 sibling 原样保留 —— 并发覆盖不丢失。

### merge(key, remote)（反熵广播合并）

1. `context = join(local, remote)`（逐节点取 max）。
2. 对两侧 sibling 按点去重，丢弃规则为：
   **仅当对方 context 覆盖该点、且对方并未携带该点作为 sibling 时才丢弃**。
   这避免了「对方 context 覆盖了点但值仍是并发分支」时的数据丢失。
3. **同值折叠**：幸存 sibling 中值相同者，确定性地保留最小点（按 `(node, counter)` 排序），
   其余点折叠进 context —— 多节点并发写入相同值只保留一个分支。
4. 合并满足幂等、交换、结合（ fuzz 测试覆盖），乱序 / 重复广播下收敛。

### 与传统版本向量的对比

| 场景 | 传统 VV | 本实现 DVV |
| --- | --- | --- |
| 客户端重试同一写入 | 产生幽灵 sibling 分支 | 同值点折叠，分支数不变 |
| 空更新（同值重写） | 分支/版本持续增长 | 单 sibling，context 仅一个计数 |
| 并发同值写入（多节点） | 值相同的冗余 sibling | 折叠为单值单点 |
| 并发异值覆盖 | sibling（正确） | sibling（正确保留） |

## API

```ts
const store = new DVVStore<string>("A");
const w1 = store.put("k", "v1");                 // -> { dot: {node:"A",counter:1}, context }
const read = store.get("k");                     // -> { values: ["v1"], context }
store.put("k", "v2", read.context);              // 因果覆盖
store.merge("k", remoteStore.entry("k"));        // 反熵合并
store.snapshot("k");                             // 可序列化快照
```

## 运行样例（`npm run demo`）

```
=== DVV dedup demo: leaderless replication, key = "profile" ===

[1] A writes 'alice', broadcast to B and C
  replica A                          ctx={"A":1}                  siblings=[(A,1)="alice"] size=2
  replica B                          ctx={"A":1}                  siblings=[(A,1)="alice"] size=2
  replica C                          ctx={"A":1}                  siblings=[(A,1)="alice"] size=2

[2] 50 empty updates on A (same value, causal rewrites)
  replica A after 50 rewrites        ctx={"A":51}                 siblings=[(A,51)="alice"] size=2

[3] retry storm: B rewrites 'alice' 5x with a STALE read context
  replica B (no ghost branches)      ctx={"A":1,"B":5}            siblings=[(B,5)="alice"] size=3

[4] concurrent conflict: B writes 'bob', C writes 'carol' (disjoint contexts)
  replica B                          ctx={"A":1,"B":6}            siblings=[(B,6)="bob"] size=3
  replica C                          ctx={"A":1,"C":1}            siblings=[(C,1)="carol"] size=3

[5] out-of-order + duplicated gossip (3 rounds, shuffled, 2x copies)
  replica A                          ctx={"A":51,"B":6,"C":1}     siblings=[(A,51)="alice" (B,6)="bob" (C,1)="carol"] size=6
  replica B                          ctx={"A":51,"B":6,"C":1}     siblings=[(A,51)="alice" (B,6)="bob" (C,1)="carol"] size=6
  replica C                          ctx={"A":51,"B":6,"C":1}     siblings=[(A,51)="alice" (B,6)="bob" (C,1)="carol"] size=6

[6] A resolves the conflict with a causal overwrite -> 'alice-2026'
  replica A                          ctx={"A":52,"B":6,"C":1}     siblings=[(A,52)="alice-2026"] size=4
  replica B                          ctx={"A":52,"B":6,"C":1}     siblings=[(A,52)="alice-2026"] size=4
  replica C                          ctx={"A":52,"B":6,"C":1}     siblings=[(A,52)="alice-2026"] size=4

converged: true
final values: ["alice-2026"]
version graph size: 4 (context entries=3, siblings=1)
```

要点：

- 步骤 [2]：50 次空更新后版本图大小恒为 2（1 个 context 项 + 1 个 sibling）。
- 步骤 [3]：5 次携带过期上下文的同值重试，全部折叠为单点 `(B,5)`，零幽灵分支。
- 步骤 [5]：乱序 + 重复 gossip 后三副本字节级一致；B 的同值点 `(B,5)` 已折叠进 context。
- 步骤 [6]：因果覆盖一次性清除全部并发 sibling，收敛到单值。

## 测试结果（`npm test`，Node 22.14 / 23.9 均通过）

```
✔ sequential writes keep a single sibling and a minimal context
✔ empty updates (same-value rewrites) never grow the version graph
✔ same-value retry with a stale read context is deduplicated (no ghost branch)
✔ different-value concurrent overwrites are preserved as siblings
✔ same dot rebroadcast is idempotent under merge
✔ concurrent same-value writes from different nodes collapse into one value
✔ causal overwrite discards dominated siblings after conflict
✔ read context supersedes everything the client has seen
✔ out-of-order duplicated broadcasts converge to a minimal graph
✔ fuzz: random leaderless workloads converge under shuffled gossip
✔ structured values dedupe by deep equality

ℹ tests 11  ℹ pass 11  ℹ fail 0
```

## 目录结构

```
src/versionVector.ts   -- 点 (Dot) 与版本向量（join / dominates / covers / fold）
src/dvv.ts             -- DVVStore：put / get / merge、同值折叠去重、确定性收敛
src/index.ts           -- 公共导出
test/dvv.test.ts       -- node:test 测试（含随机化乱序 gossip 收敛 fuzz）
examples/demo.ts       -- 无主复制 + 乱序广播运行样例
```
