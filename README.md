# order-saga-executor

本地订单流程（预留库存 → 扣款 → 安排配送）的持久化 saga 执行器，TypeScript 编写，
零依赖，只用 Node 内置模块。所有外部动作通过可注入的合成适配器调用
（`InventoryAdapter` / `PaymentAdapter` / `DeliveryAdapter`），不接真实支付或物流。

要求 Node >= 23.6（原生 TypeScript 类型擦除 + `node:test`）。

```sh
nvm use 23          # 或任何 >= 23.6 的 Node
npm test            # node --test test/*.test.ts
npm run replay      # 确定性故障重放，输出应对照 replay-results.txt
```

## 结构

- `src/journal.ts` — 追加式日志：CRC-32 帧、write+fsync、重放校验、撕裂尾部截断
- `src/executor.ts` — saga 执行器：意图/结果日志、恢复对账、逆序补偿、唯一终态
- `src/adapters.ts` — 合成适配器：幂等键、回执查询、确定性故障脚本
- `src/order-workflow.ts` — 订单流程组装（reserve → charge → schedule）
- `test/` — Node 内置测试（31 个用例）
- `scripts/replay-faults.ts` + `replay-results.txt` — 可重放故障脚本与结果

## 文件协议与持久化确认点

日志是逐行记录，每行一条：

```
<crc32-hex-8> <json-body>\n
```

`json-body = {"seq":N,"type":T,"workflowId":W,"data":{...}}`，CRC 覆盖 body 原始字节。

**持久化确认点**：`Journal.append()` 在一次 `write(2)` 写入整行后紧跟 `fsync(2)`，
两者都完成才返回并把记录发布到内存索引。只有 `append()` 正常返回的记录才被视为
已持久化。任何其结果记录尚未确认的动作，恢复时一律视为**结果未知**（而不是失败），
必须先按幂等键向适配器查询对账。

**恢复规则**：

1. 文件不存在或为空 → 零条记录。
2. 文件末尾不是 `\n` → 最后一条是崩溃时的撕裂写，`open()` 会把文件截断到最后一条
   完整记录处。只容忍这最后一条不完整记录。
3. 其余每条完整记录必须通过帧格式、CRC、JSON 解析和 seq 连续校验；任何违反都抛
   `JournalCorruptionError`——完整的坏记录报错，绝不静默跳过。

## 执行语义

- 每个动作先写 `intent` 再调用适配器，完成后写 `result`；补偿同样写
  `intent`/`result`（`phase: "compensate"`），日志因此完整记录动作意图、结果与补偿进度。
- 动作与补偿都有稳定幂等键：`order:<id>:<step>` 与 `...:compensate`。适配器可模拟
  “已执行但回执丢失”（`lost-receipt` 故障）：状态已提交、响应丢失。恢复时执行器先
  `query(幂等键)` 对账：有回执则补记成功结果，无回执才在同一幂等键下重发。
- 失败时只补偿**确已成功**的动作，严格逆序；补偿的暂时失败
  （`WorkflowPausedError`）可恢复——重跑 `run()` 从断点继续，已完成的补偿靠日志
  与适配器幂等键保证不重复改变业务状态。
- 终态为 `completed | compensated | cancelled`，由同步的 check-and-append
  （`tryTerminal`）写入，全日志唯一。取消与最终成功竞争时只产生一个终态：
  取消记录先落盘则走补偿后 `cancelled`；终态先落盘则 `cancel()` 返回既有终态、
  不再产生任何记录。重复取消幂等（只记一条 `cancel`）。

## 测试与故障重放

- `test/journal.test.ts` — 持久化确认、撕裂尾部容忍、完整坏记录报错、seq 空洞报错
- `test/executor.test.ts` — 正常流、业务失败逆序补偿、回执丢失对账、补偿失败恢复、
  已完成补偿不重复
- `test/recovery.test.ts` — 在全部 8 个持久化边界逐一崩溃重启、连续多次崩溃、
  补偿中途崩溃，均验证库存/金额/配送与流程终态一致
- `test/cancellation.test.ts` — 开始前/进行中取消、取消与成功竞争（两种时序）、
  重复取消、重启后兑现取消
- `scripts/replay-faults.ts` — 6 个确定性故障场景（S1–S6），输出已固化在
  `replay-results.txt`，重跑应逐行一致（首行时间戳除外）
