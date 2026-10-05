# 持久化订单流程执行器（Saga）

本地订单流程：**预留库存 → 扣款 → 安排配送**，失败时逆序补偿
（取消配送 → 退款 → 释放库存）。所有外部动作通过可注入的合成适配器调用，
**不接真实支付或物流**。

## 结构

- `src/journal.ts` — 只追加 WAL：每行一条 JSON 记录 + CRC32，`write`+`fsync`
  后才确认持久化；恢复时截断尾部撕裂记录、完整坏记录报错（详见 `PROTOCOL.md`）。
- `src/adapters.ts` — 合成适配器（库存/支付/配送）：幂等键去重、`query(key)`
  对账、确定性故障注入（`drop-receipt` 已执行但回执丢失 / `reject` 明确拒绝 /
  `transient` 暂时失败，可按操作名定向）。
- `src/executor.ts` — 流程执行器：动作意图/结果、补偿意图/结果全部落日志；
  恢复先按幂等键查询未知结果；只补偿确已成功的动作且逆序；补偿暂时失败
  停留在 `compensating` 可恢复；终态单赋值，取消与成功竞争只产生一个终态。
- `src/order-flow.ts` — 订单流程编排与业务一致性快照（库存/金额/配送 ↔ 流程终态）。
- `test/saga.test.ts` — Node 内置测试（`node:test`）。
- `scripts/fault-replay.ts` — 可重放故障脚本；`fault-replay-results.txt` 为其运行结果。

## 运行

需要 Node ≥ 23（原生类型擦除运行 TypeScript；Node 22.6+ 可加
`--experimental-strip-types`）：

```sh
npm test            # node --test test/saga.test.ts
npm run fault-replay  # node scripts/fault-replay.ts
```

## 测试覆盖

- 在每个持久化边界（每条日志记录之后）模拟崩溃重启，恢复后流程到达
  `completed`（成功路径）或 `failed`（扣款被拒路径），且每个动作/补偿
  恰好生效一次；
- 回执丢失（动作与补偿各一例）：恢复时按幂等键对账，不重复扣款/释放；
- 补偿暂时失败 → 停留在 `compensating`，恢复后完成补偿；
- 已完成的补偿在重复恢复时不重复改变业务状态；
- 重复取消、取消与最终成功竞争：日志中恰有一条 `terminal` 记录；
- 文件协议：尾部撕裂记录被截断；完整坏记录抛 `JournalCorruptionError`；
- 每个场景断言库存预留量、支付金额、配送状态与流程终态一致。
