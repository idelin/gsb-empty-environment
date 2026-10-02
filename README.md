# 协程池：优先级抢占 + 时间片轮转

零依赖 TypeScript 实现的多优先级协同式协程池，使用 Node.js 内置测试框架（`node:test`）验证。

## 调度语义

- **严格优先级**：每个协同出让点（`yield`）之后，CPU 交给就绪队列中优先级数值最高的协程。
- **优先级抢占**：高优先级协程可在低优先级协程主动出让时插队；低优先级协程每执行一步都会重新检查就绪队列，一旦出现更高优先级协程立即强制协同切换（`preempt: higher-priority-ready`），严格控制优先级反转。
- **时间片轮转**：同优先级协程按 FIFO 轮转；单个协程连续执行超过 `timeSlice` 步后被强制换出到队尾（`preempt: timeslice-exhausted`）。
- **配额监控与活性**：调度器统计每个协程的执行步数（`stats()`），时间片配额保证长任务不会饿死其他协程，`run()` 在所有协程完成后终止。

## 环境要求

- Node.js ≥ 23.6：直接运行 `.ts`（内置类型擦除）。
- Node.js ≥ 22.6：需加 `--experimental-strip-types`，即
  `node --experimental-strip-types --test` / `node --experimental-strip-types examples/demo.ts`。

## 运行

```bash
npm test    # node --test，运行 test/scheduler.test.ts
npm run demo # 运行 examples/demo.ts，打印完整调度轨迹
```

## 验收场景

协程 A 优先级 1、协程 B 优先级 3。B 处于就绪态时，A 执行一次 `yield`
立即将 CPU 让给 B；B 执行完毕后 A 继续运行。样例输出节选：

```
▶ dispatch  A (p1) 获得 CPU
  step      A 执行第 1 步
  yield     A 主动出让
  preempt   A (p1) 被更高优先级协程抢占
▶ dispatch  B (p3) 获得 CPU
  ...
■ complete  B (p3) 执行完毕
▶ dispatch  A (p1) 获得 CPU
  ...
■ complete  A (p1) 执行完毕
```

## 文件结构

- `src/scheduler.ts` — 协程池实现（`CoroutinePool`）
- `test/scheduler.test.ts` — Node.js 内置测试（5 个用例）
- `examples/demo.ts` — 可运行样例，打印调度轨迹与统计
