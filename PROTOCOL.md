# 日志文件协议（Journal Protocol）

流程执行器使用单文件、只追加的预写日志（WAL），路径由调用方指定（如 `flow.journal`）。

## 记录格式

- 每条记录是**一行 JSON**，以 `\n` 结尾，字段包括：
  - `seq`：单调递增序号（从 1 开始）；
  - `ts`：写入时间戳（毫秒，仅用于审计，不参与恢复逻辑）；
  - `type`：记录类型，见下；
  - 业务负载字段（如 `step`、`key`、`status`、`receipt`、`error`、`via`）；
  - `crc`：对**去掉 crc 字段后**的规范 JSON 串计算的 CRC32，用于区分"完整记录"与"坏记录"。
- 记录类型：`flow_start`、`action_intent`、`action_result`、
  `compensation_intent`、`compensation_result`、`cancel_requested`、`terminal`。

## 何时确认持久化

**一条记录只有在 `Journal.append()` 返回时才被视为已持久化。** `append()` 内部依次执行
`write(2)` 和 `fsync(2)`，两者都成功后才返回（见 `src/journal.ts` 中标注
"durability confirmation point" 的位置）。执行器的纪律是：

- 先写 `action_intent`（并 fsync），**之后**才调用适配器执行外部动作；
- 适配器返回后，先写 `action_result`（并 fsync），**之后**才推进到下一步；
- 补偿同理：`compensation_intent` → 调用适配器 → `compensation_result`；
- `terminal` 是单赋值记录：写入前重新检查日志，已存在终态则放弃写入，
  因此取消与最终成功竞争时只会产生一个终态。

凡是 `append()` 未返回的记录，崩溃后一律视为不存在（可能被截断）。

## 崩溃恢复规则

恢复时从头顺序解析：

1. **截断只容忍最后一条不完整记录**：文件末尾若存在未以 `\n` 结尾的行，
   视为最后一次写入的撕裂（torn write），直接截断该尾部并继续；
2. **完整坏记录报错**：任何以 `\n` 结尾但 JSON 解析失败、CRC 校验失败或
   类型不合法（不在已知类型集合内）的记录，抛出 `JournalCorruptionError`，
   恢复中止，绝不静默跳过；
3. 恢复出的记录序列是执行器重建流程状态的唯一依据。

## 未知结果的恢复语义

- 适配器"已执行但回执丢失"时，执行器记录 `action_result{status:"unknown"}`
  （或崩溃导致只有 `action_intent` 没有结果），流程挂起；
- 恢复时**先按幂等键调用适配器的 `query(key)` 对账**：
  - 下游确认已执行 → 补记 `action_result{status:"ok", via:"recovery-query"}`；
  - 下游确认未执行 → 用同一幂等键安全重试该动作（幂等键保证不会重复生效），
    **绝不**在未经对账的情况下直接当失败进入补偿；
- 补偿的未知结果按同样规则处理（`compensation_result` + 对账）。

## 幂等键

- 动作键：`flow:<flowId>:step:<stepName>`；
- 补偿键：`flow:<flowId>:step:<stepName>:comp`。

适配器以幂等键去重：同一键重复调用返回已存回执、不再改变业务状态，
因此"已经完成的补偿"在恢复重放时不会重复改变库存/金额/配送状态。
