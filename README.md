# crdt-replica-bus

用可控消息总线模拟多副本复制：操作带唯一 ID 与因果版本（向量时钟），合并规则显式声明，
重复消息与断线重放幂等不双应用。零依赖，TypeScript 源码由 Node ≥ 23.6 原生运行（类型擦除），
测试使用 Node 内置 `node:test`。

## 运行

```sh
npm test    # node --test 'test/*.test.ts'
npm run demo
```

## 设计

| 需求 | 实现 |
| --- | --- |
| 可控消息总线 | `src/bus.ts`：消息入队、`deliver()` 显式投递、`partition()/heal()` 模拟分区、`injectDuplicate()` 注入重复、保留日志供重连重放 |
| 操作带 ID 和因果版本 | `src/ops.ts`：每个 `Op` 含全局唯一 `opId`、Lamport 时钟与向量时钟 `clock` |
| 合并规则显式 | `Schema` 按字段声明：`lww`（按 `(lamport, replicaId)` 全序决胜）或 `mv` 多值寄存器（`preserve` 保留冲突 / `max` / `min` 决胜） |
| 重复消息不双应用 | `src/store.ts`：`appliedOpIds` 去重，重复 `opId` 直接跳过 |
| 断线重放不双应用 | 重连后 `syncFrom()` 重放总线保留日志，命中同一去重集合 |
| 状态操作可回放 | `Store.replay(schema, opLog)` 从批次日志重建等价状态（静默、不触发副作用） |
| 读者看不到事务中间态 | 事务写入先缓冲在 `Transaction`，commit 时作为单个 `Batch` 原子应用；`snapshot()` 只读已提交状态并返回冻结副本 |
| 不同字段可合并 | 字段级寄存器互不影响，并发写不同字段自动合并 |
| 同字段冲突 | LWW 确定性决胜；MV 保留全部并发值（`conflicts()` 可查）或按 `max`/`min` 规则决胜；因果覆盖的写会取代旧值 |
| 本地回声不重复触发副作用 | 总线把消息回投发送者，回声因 `opId` 已应用被跳过，副作用监听器只在首次应用时触发 |

## 结构

- `src/version.ts` — 向量时钟：tick / merge / 因果支配比较
- `src/ops.ts` — 操作、事务批次、合并规则定义
- `src/store.ts` — 副本存储：幂等应用、LWW/MV 寄存器、快照、重放
- `src/bus.ts` — 可控消息总线
- `src/replica.ts` — 副本：事务、广播、接收、断线同步、副作用钩子
- `test/replication.test.ts` — 10 个用例覆盖上述全部需求
- `demo/main.ts` — 端到端运行样例
