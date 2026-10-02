# MVCC 内存存储引擎

单进程多版本并发控制（MVCC）存储引擎，TypeScript 实现，零依赖，
使用 Node.js 内置测试运行器（`node:test`）验证。

## 核心语义

- **单调自增事务 ID**：`begin()` 时分配，同时固定一致性快照（snapshotTs）。
- **快照隔离读**：事务只能看到「快照时刻之前已提交」的最新版本，
  以及自己的未提交写入（read-your-own-writes）。
- **First-Committer-Wins 写写冲突**：
  1. 写时检测：目标键存在其他事务的未提交版本，或快照之后已有已提交新版本 → 立即 Abort；
  2. 提交时校验：写集中的键在快照之后被其他事务提交 → 首提交者获胜，当前事务 Abort。
- 未提交版本即时挂上版本链（`commitTs = null`）充当"写锁"，
  Abort / Rollback 时摘除，提交时原地打上提交时间戳。

## 环境要求

Node.js >= 22.6（推荐 >= 23.6，可省略 `--experimental-strip-types` 标志），
利用 Node 原生 TypeScript 类型擦除直接运行 `.ts`，无需编译、无需安装依赖。

## 运行

```bash
npm test    # 运行全部测试（node:test）
npm run demo  # 运行验收场景样例
```

## 验收场景（examples/demo.ts 输出）

```
[seed ] txn 9 committed: K1 = 'v0'
[T1   ] txn 10 wrote K1 = 'v1' (uncommitted)
[T2   ] txn 11 read K1 -> 'v0' (snapshot: 只见旧版本)
[T2   ] txn 11 ABORTED (write-write conflict): txn 11 aborted: write-write conflict on key "K1": first-writer txn 10 wins
[T1   ] txn 10 committed
[T3   ] txn 12 read K1 -> 'v1' (读到 T1 提交的最新值)

版本链 K1: [
  { txnId: 9, value: 'v0', commitTs: 1 },
  { txnId: 10, value: 'v1', commitTs: 2 }
]
```

## 测试结果

```
✔ 验收场景：T1(10) 未提交写对 T2(11) 不可见，T2 写冲突被 Abort，T1 提交后 T3(12) 读到新值
✔ 事务 ID 单调自增
✔ read-your-own-writes：事务能读到自己的未提交写入
✔ 快照隔离：快照后其他事务的提交对老事务不可见
✔ First-Committer-Wins：提交时校验，首提交者获胜
✔ 写写冲突被 Abort 后，其未提交版本被清除，不污染版本链
✔ rollback：未提交写入对外不可见且被移除
✔ delete：墓碑对快照读表现为键不存在
✔ 不同键互不冲突，可并行提交
✔ 已结束事务不允许再操作
ℹ tests 10  ℹ pass 10  ℹ fail 0
```

## 目录结构

- `src/mvcc.ts` — MVCC 引擎（`MvccStore` / `Transaction` / `TransactionAbortedError`）
- `test/mvcc.test.ts` — `node:test` 测试（含完整验收场景）
- `examples/demo.ts` — 验收边界运行样例（T1=10 / T2=11 / T3=12）
