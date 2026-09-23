# MVCC + OCC 事务键值存储（TypeScript）

空仓库从零实现的多版本并发控制（MVCC）存储 + 乐观并发控制（OCC）事务。
不依赖任何响应式/数据库框架，仅用 TypeScript 标准库，用 Node.js 内置测试运行器
（`node --test`）验证。

## 运行环境

- Node.js ≥ 22.6（直接运行 `.ts`，利用内置 TypeScript type-stripping；本仓库在 v23.9.0 验证）
- 无需 `npm install`，无第三方运行时依赖

```bash
npm test          # node --test test/mvcc.test.ts
npm run demo      # node examples/demo.ts
npm run typecheck # 可选：npm i -D typescript @types/node && tsc --noEmit
```

## 核心设计

- **版本链**：每个 key 对应一个按提交时间戳升序排列的不可变版本数组
  `{ ts, txnId, value, deleted }`，删除即写入墓碑（tombstone）。
- **固定快照**：事务在 `begin()` 时记录 `readTs = 当前已提交时间戳`。所有读操作
  都走该快照（二分查找 `ts <= readTs` 的最新版本）叠加本事务缓冲写，因此
  **读者永远看不到其他事务的中间态**，也不受后续提交影响（旧读者不看新版本）。
- **提交前校验读集（first-committer-wins）**：提交时在串行化临界区内检查读集
  （以及盲写的写集）中每个 key 的最新版本是否 `ts > readTs`。有冲突则整体拒绝，
  缓冲写全部清空——**冲突失败即原子回滚**，不会留下部分写入。
- **提交原子性**：校验通过后分配单调递增的 `commitTs`，该事务的所有写以同一时间戳
  一次性安装，其他读者要么看到事务前状态，要么看到事务后状态。
- **可回放**：每次提交向只增操作日志追加一条 `LogRecord`（含 commitTs 与全部写）。
  `MVCCStore.replay(log)` 可重建出逐版本一致的状态，且与记录顺序无关（按 ts 排序）。
- **只读快照**：`beginReadOnly()` 不分配写时间戳、提交不推进全局时钟；长事务只读
  视图在其存活期内保持一致。
- **垃圾回收**：`garbageCollect()` 以所有存活事务的最小 `readTs`（floor）为界：
  - `ts > floor` 的版本全部保留（最老的读者之后的版本可能还要看）；
  - `ts <= floor` 的版本只保留最新一个（即该快照实际读到的版本）；
  - 若某 key 保留下来的版本全部 `<= floor` 且最新版本是墓碑，则整个 key 删除；
  - 操作日志不受 GC 影响，仍可用于完整重建。
- **提交串行化**：Node 单线程，但提交跨越 `await`，故用 Promise 互斥链保证
  “校验 + 安装”原子执行。

## 并发保证（对应测试）

| 场景 | 行为 |
| --- | --- |
| 两事务读同值后各自写 | 先提交者成功，后提交者 `read-write conflict` 中止，绝不丢更新 |
| 两事务盲写同一 key | 同样检测 `write-write conflict`，不无声覆盖 |
| 不同 key 的事务 | 互不冲突，均可提交（同一提交点串行分配时间戳） |
| 长生命周期旧读者 | 快照固定，期间的新提交对其不可见 |
| 未提交写 | 仅本事务可见；回滚后无任何痕迹 |
| GC | 只删任何存活/未来读者都不可能看到的版本，最老读者视图不受损 |

`runTransaction(store, fn, { retries })` 提供冲突自动重试：在冲突时用全新快照
重新执行 `fn`（操作可回放的典型用法）。

## 代码结构

- `src/mvcc.ts` — `MVCCStore`、`Txn`、`runTransaction`、日志与 GC
- `test/mvcc.test.ts` — 17 个 `node:test` 用例，6 个分组
- `examples/demo.ts` — 8 段可运行场景演示

## 运行样例（`npm run demo` 实际输出）

```
=== 1. Basic read/write transaction ===
committed balance=100 inventory=5, commit ts = 1

=== 2. Lost update: two txns read 100, both write ===
Alice (-30) committed; balance = 70
Bob (-50) rejected -> transaction aborted: read-write conflict on key "balance"
Final balance is 70 (no silent lost update)

=== 3. Retry helper: 5 concurrent increments all survive ===
counter after 5 concurrent increments = 5

=== 4. Different keys never conflict ===
both committed, commit ts = 10

=== 5. Snapshot isolation: an old reader ignores newer commits ===
reader opened at ts=11, sees k = 1
after two more commits, old reader still sees k = 1
a fresh reader sees k = 3

=== 6. Uncommitted writes are invisible to other readers ===
writer sees its own buffered k = 999
other reader sees committed k = 3
after rollback k = 3

=== 7. Garbage collection is bounded by active readers ===
versions of v before GC: 4
GC with no readers removed 3 versions; remaining: 1
GC with reader at ts=6 removed 2 version(s); old reader still sees v = 6 ; latest v = 7

=== 8. Replaying the operation log rebuilds the store ===
rebuilt ts 13 == original ts 13
rebuilt versions 14 == original versions 14
rebuilt balance = 70 , counter = 5 , k = 3
```

## 测试结果（`npm test`，Node v23.9.0）

```
▶ basic commit / read
  ✔ a commit becomes visible to later transactions
  ✔ atomic multi-key commit
  ✔ delete creates a tombstone
▶ snapshot isolation
  ✔ old reader never sees newer committed versions
  ✔ reader cannot see intermediate uncommitted state
  ✔ read-only snapshot sees a coherent multi-key point in time
▶ optimistic conflict detection
  ✔ two txns reading the same value cannot silently lose an update
  ✔ different keys do not conflict and both commit
  ✔ blind writes on a key changed since snapshot also conflict
  ✔ committing after rollback is rejected; buffered writes are gone
  ✔ read-only transaction rejects writes
▶ retry helper
  ✔ runTransaction retries conflicts and both increments survive
▶ garbage collection
  ✔ retains only the version visible to the oldest active reader
  ✔ GC with no active readers keeps only the latest version
  ✔ GC removes keys whose latest retained version is a tombstone
▶ operation log replay
  ✔ replaying the log reconstructs identical versioned state
  ✔ replay is order independent (records sorted by commitTs)
ℹ tests 17
ℹ pass 17
ℹ fail 0
```

## 使用示例

```ts
import { MVCCStore, runTransaction } from './src/mvcc.ts';

const store = new MVCCStore<number>();

await runTransaction(store, (t) => t.set('balance', 100));

await runTransaction(
  store,
  (t) => t.set('balance', (t.get('balance') ?? 0) - 30),
  { retries: 3 }, // 冲突时用新快照自动重放
);

const snap = store.beginReadOnly();
console.log(snap.get('balance')); // 70，且该视图永久固定在 begin 时刻
await snap.commit();

store.garbageCollect();                 // 按存活读者回收旧版本
const copy = MVCCStore.replay(store.getLog()); // 从操作日志重建
```
