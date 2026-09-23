# controlled-bus-replicas

用可控消息总线模拟多副本复制的最小实现。纯 TypeScript（Node >= 22.6 原生类型擦除运行），
无第三方依赖、无响应式框架，测试使用 Node 内置 `node:test`。

## 运行

```sh
npm test     # node --test test/*.test.ts
npm run demo # node demo.ts
```

## 设计

- `src/bus.ts` — 可控消息总线。`publish` 只追加到持久日志，不自动投递；
  `flush()` 显式投递。每个订阅者记录偏移量，`disconnect`/`reconnect` 后按偏移
  重放错过的消息，天然支持断线重放。
- `src/operations.ts` — 操作携带 `id`（`副本#事务序号/下标`）、`txnId`、`txnSize`、
  因果版本（向量时钟 `clock`）与 LWW 时间戳 `ts`。
- `src/vectorClock.ts` — 向量时钟：`tick`/`mergeClocks`/`compare`（before/after/
  equal/concurrent）/`causallyReady`（因果就绪判定）。
- `src/merge.ts` — 显式合并规则，按字段配置：
  - `lww`：并发写按 `(ts, replicaId)` 确定性决胜，保留单一赢家；
  - `multi-value`：并发版本全部保留，被因果支配的版本自动移除；
  - 因果上更晚的写（`after`）总是覆盖旧版本，与策略无关。
- `src/replica.ts` — 副本核心：
  - `transact()` 把一组 set/delete 打包成一个事务，共享同一因果版本，
    先在本地原子提交再发布；
  - 事务以整体为单位提交（先在草稿上应用再一次性换入），读者经
    `snapshot()`/`get()` 只能看到提交后的完整状态，观察者每个事务只通知一次；
  - 因果未就绪或事务不完整的消息进入缓冲区，依赖到达后自动 drain；
  - `applied` 集合按操作 ID 去重：重复投递、断线重放、本地回声都只应用一次，
    副作用（`onCommit`）不会重复触发；
  - `txnLog` 记录全部已提交事务，`rebuildFromLog()` / `replayLog()` 可确定性
    回放重建状态。

## 测试覆盖（15 个用例）

向量时钟语义；不同字段并发编辑合并；同字段 LWW 决胜（含时间戳并列时按
replicaId 决胜）；multi-value 保留并发值；因果晚写覆盖；事务原子可见性；
乱序投递缓冲；重复消息幂等；本地回声不重复触发副作用；断线重放不双应用；
日志回放重建一致性；删除作为因果墓碑。
