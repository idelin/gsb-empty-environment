# virtual-periodic-scheduler

虚拟时钟驱动的周期调度器（TypeScript）。时钟只由测试显式推进，调度行为完全确定。

## 语义

- **固定起点网格**：触发点固定在 `startAt + k * periodMs`（k ≥ 1）的网格上，网格相位永不漂移。
- **超时合并补偿**：任务运行时间超过周期时，期间漏掉的所有触发只合并为**一次**补偿运行
  （`kind: 'catch-up'`，携带 `missedCount`），当前任务结束后立即执行；补偿结束后调度回到原时间网格。
- **暂停不累计**：`pause()` 期间流逝的网格点被丢弃；`resume()` 后仅等待下一个未来网格点，不会补发。
- **停止**：`stop()` 取消后续触发；正在执行的回调继续跑完，可用 `idle()` 等待其结束。
- **失败不致命**：回调抛错或 Promise 拒绝时通过 `onError` 上报，调度照常继续（含补偿）。

## 运行测试

需要 Node ≥ 22.6（利用内置 type-stripping 直接运行 `.ts`，无需编译）：

```sh
npm test
```

## 结构

- `src/clock.ts` — `VirtualClock`：确定性虚拟时钟，`advanceBy`/`advanceTo` 按（时间、插入序）触发定时器。
- `src/scheduler.ts` — `PeriodicScheduler`：固定网格周期调度，合并补偿、暂停/恢复、停止语义。
- `test/` — 基于 `node:test` 的测试，覆盖暂停恢复、长任务失败、停止时正在执行的回调等场景。
