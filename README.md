# Sliding Window Notification Delivery

TypeScript 实现的有限滑动窗口投递确认库：接收方可并发乱序完成，水位只越过连续完成的序号，失败项进入重投列表，重复完成幂等，旧代次确认无法确认新投递。

## 核心语义

- **投递 (`deliver`)**：分配单调递增序号；当 `nextSeq - 1 - watermark >= windowSize` 时拒绝（`window-full`），窗口绝不越界。
- **完成 (`complete(seq, generation)`)**：
  - `seq <= watermark`：幂等成功，无副作用（重复确认）。
  - 代次不匹配：拒绝（`stale-generation`），旧代次确认不能确认新投递。
  - 非 pending 状态（已确认/已失败）：幂等成功，无副作用。
  - 确认后水位仅越过连续已确认的序号，空洞不跨越。
- **超时 (`timeout`)**：pending 项标记为 failed 并进入重投列表；重复超时拒绝（`not-pending`）。
- **重投 (`redeliver`)**：仅 failed 项可重投，代次 +1，此后旧代次确认一律失效。
- 已越过水位的槽位被回收，内存随窗口有界。

## 输出

每次 `apply(event)` 返回 `StepResult`：`accepted` / `reason` / `seq` / 当前 `watermark` / 当前 `redeliveries` 快照。`drainRedeliveries()` 取出并清空重投列表。

## 运行验证

```sh
tsc -p tsconfig.json && node dist/verify.js
```

`src/verify.ts` 覆盖：乱序完成与空洞、重复完成幂等、超时重投与代次隔离、窗口上界拒绝，并在混合事件流的每一步断言不变量 `0 <= watermark < nextSeq` 与 `nextSeq - 1 - watermark <= windowSize`。
