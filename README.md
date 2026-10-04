# msgproc — 两层状态消息处理库

签收（Receipt）与业务执行（Business）分开记录：**签收成功 ≠ 业务成功**。

## 两层状态

- 签收层：`ACKED` — 消息已受理，签发处理票据 `Ticket`。
- 业务层：`PENDING → RUNNING → SUCCEEDED / FAILED / TIMEOUT / RESULT_LOST`。

由两层状态推导出面向调用者的三阶段（`StatusView.Phase()`）：

| 阶段 | 含义 | 对应业务状态 |
|---|---|---|
| `PhaseAcknowledged` | 已签收，业务无确定结果 | `PENDING` / `RUNNING` |
| `PhaseResultUnknown` | 执行过但结果丢失 | `RESULT_LOST` |
| `PhaseResultKnown` | 结果已确定 | `SUCCEEDED` / `FAILED` / `TIMEOUT` |

## 接口

- `Acknowledge(msg)` — 按 `Message.ID` 幂等签收，重复签收返回同一票据。
- `Execute(ctx, ticketID, handler)` — 执行一次业务，每次调用追加一条 `Attempt`，重试历史完整保留。
- `Query(ticketID)` — 返回两层状态、尝试历史与结果（若已知）。

`Store` 独立于 `Processor`：崩溃恢复后用同一 `Store` 重建 `Processor` 即可继续。

## 故障注入（`WithFaults`）

- `CrashAfterAck` — 签收持久化后模拟崩溃（返回 `ErrSimulatedCrash`），业务停留在 `PENDING`。
- `HangInExecute` — 业务挂起直至超时，尝试记为 `TIMEOUT`。
- `LoseResult` — 业务成功但结果丢弃，状态转为 `RESULT_LOST`。

## 测试

```sh
go test -race ./...
```
