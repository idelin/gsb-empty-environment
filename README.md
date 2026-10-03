# mvcc-store

内存快照隔离 MVCC（多版本并发控制）存储引擎，TypeScript 实现，零依赖。

## 特性

- **单调自增事务 ID**：`begin()` 为每个事务分配递增 ID
- **快照隔离读**：事务启动时固定快照（`snapshotTs`），只能读到 `commitTs <= snapshotTs` 的最新已提交版本；支持 read-your-own-writes
- **首写获胜（First-Committer-Wins）**：写写冲突在写入与提交两个时点检测，冲突事务立即中止（Abort）
  - 其他活跃事务持有同一 key 的未提交写 → 冲突
  - 同一 key 在本事务快照之后出现新提交版本 → 冲突

## 环境要求

Node.js >= 22.6（使用内置 TypeScript 类型擦除与 `node:test`，无需安装任何依赖）。

## 运行

```bash
npm test    # 运行 node:test 测试
npm run demo  # 运行验收场景演示
```

## 演示输出（`npm run demo`）

```
初始状态: K1 = "v0" (已提交)

T1 (ID=10) 开始
T1 (ID=10) 写入 K1 = "v1"（未提交）

T2 (ID=11) 开始
T2 (ID=11) 读 K1 => "v0"（快照隔离，只见旧版本）
T2 (ID=11) 写 K1 => 写写冲突，事务被 Abort
T2 (ID=11) 状态: aborted

T1 (ID=10) 提交成功（首写获胜）

T3 (ID=12) 开始
T3 (ID=12) 读 K1 => "v1"（读到 T1 提交的最新值）
```

## 测试结果（`npm test`）

```
✔ 验收场景：快照隔离读 + 写写冲突首写获胜
✔ 单调自增事务 ID
✔ 快照隔离：读不到事务启动后提交的版本
✔ 读自己的未提交写（read-your-own-writes）
✔ 首写获胜：提交时发现快照后被他人提交则中止
✔ 写不同 key 的并发事务互不冲突
✔ 已结束事务不可再读写
✔ 读取不存在的 key 返回 undefined
ℹ tests 8  ℹ pass 8  ℹ fail 0
```

## 设计要点

- 每个 key 维护一条按提交时间戳递增的版本链；未提交的写缓冲在事务的写集合中，中止时直接丢弃，无需回滚版本链
- 写意图登记表（`writeIntents`）跟踪活跃事务的未提交写，用于写入时点的写写冲突检测
- 提交时点再次校验首写获胜，防止写入后、提交前他人抢先提交同一 key
- 预置数据通过 `seed` 选项以 `commitTs = 0` 的引导版本写入，不占用事务 ID

## API

```ts
const store = new MVCCStore({ firstTxnId: 10, seed: { K1: 'v0' } });
const tx = store.begin();          // 开启事务，分配 ID 与快照
store.read(tx, 'K1');              // 快照读
store.write(tx, 'K1', 'v1');       // 缓冲写；冲突时抛 WriteConflictError 并中止
store.commit(tx);                  // 提交；冲突时抛 WriteConflictError 并中止
store.abort(tx);                   // 主动中止
```
