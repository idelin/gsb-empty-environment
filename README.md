# fiber-debugger

可暂停与逐步单步步进的协程（Fiber）调试器 —— 对 Fiber 调度过程的控制反转（Inversion of Control）调试器，便于自动化测试排错。

## 特性

- **控制反转调度**：调度器不自旋，全局时钟以 tick 为单位，每一步都由调试器显式推动
- **单步调试模式**：`enterStepMode()` 暂停全局时钟，之后只能 `dbg.tick()` 逐 Tick 手动推动
- **断点**：按 `协程 + pc` 设置断点，命中即暂停该协程；`continue()` 恢复
- **指令级单步**：`stepInstruction(fiber)` 只执行某个协程的一条指令，不推进全局时钟
- **挂起原因字典**：`suspendReasons()` 查看所有协程的挂起原因（等待锁 / 等待 Channel / 睡眠 / 等待资源 / 断点）
- **状态转换字典**：每次状态迁移都记录 `{tick, fiber, from, to, pc, note}`，支持按协程 / 按 tick 查询、按 tick 重放系统状态
- **假死检测**：`detectStall(minTicks)` 报告在特定时隙陷入挂起且长期无状态转换的协程

## 环境要求

- Node.js ≥ 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，零依赖、零构建）

## 运行

```bash
npm test    # node --test test/*.test.ts
npm run demo # node examples/livelock-demo.ts
```

## 快速上手

```ts
import { Scheduler, Debugger, sleep, requireResource, provideResource } from './src/index.ts';

const scheduler = new Scheduler();
const dbg = new Debugger(scheduler);

scheduler.spawn('db-client', function* () {
  yield requireResource('db-config'); // 等待资源初始化
});
scheduler.spawn('initializer', function* () {
  yield sleep(2);
  yield provideResource('db-conf');   // Bug：资源名写错，db-config 永远无人提供
});

dbg.enterStepMode();   // 暂停全局时钟
dbg.tick();            // 逐 Tick 手动推动
dbg.tick();
dbg.detectStall(2);    // → db-client 自 tick=1 起停滞：等待资源 "db-config" 初始化
```

## 指令集

| 指令 | 语义 |
| --- | --- |
| `nop()` | 空操作 |
| `sleep(ticks)` | 睡眠指定 tick 数 |
| `lock(name)` / `unlock(name)` | 互斥锁（竞争时排队挂起） |
| `send(ch, v)` / `recv(ch)` | 会合（无缓冲）Channel |
| `requireResource(name)` / `provideResource(name, v)` | 等待 / 提供命名资源 |
| `spawn(name, body)` | 动态派生协程 |

## 运行样例输出（`npm run demo`）

```
[时钟已暂停] 全局时钟已被调试器暂停（tick=0），请通过调试器推动

=== 逐 Tick 手动推动调度器 ===
tick=1 stepMode=true
  #1 db-client: waiting-resource pc=1 | 等待资源 "db-config" 初始化
  #2 initializer: sleeping pc=1 | 睡眠至 tick=3
  #3 heartbeat: ready pc=1
    └─ db-client: running → waiting-resource (require("db-config") → 资源未初始化)
...
=== 假死检测报告 ===
  ⚠ db-client 自 tick=1 起停滞 4 个 tick（waiting-resource）：等待资源 "db-config" 初始化
=== 定位后修复：补齐资源初始化 ===
db-client 最终状态: done
```

## 测试结果（`npm test`，Node v23.9.0）

```
✔ 单步调试模式：暂停全局时钟，仅允许调试器逐 Tick 手动推动
✔ 断点：命中后暂停协程，可单步执行该协程的一条指令并继续
✔ 挂起原因字典：睡眠 / 等待锁 / 等待Channel / 等待资源
✔ 会合 Channel 与互斥锁的移交顺序确定
✔ 验收：状态转换字典定位"等待未初始化资源"导致的假死
ℹ tests 5
ℹ pass 5
ℹ fail 0
```

## 代码结构

- `src/instructions.ts` — 协程指令集（Generator yield 的 opcode）
- `src/fiber.ts` — Fiber 状态机与挂起原因类型
- `src/scheduler.ts` — 离散时间调度器：时钟、锁、Channel、资源、状态迁移事件
- `src/debugger.ts` — 调试器：断点 / 单步 / 状态转换字典 / 假死检测
- `test/debugger.test.ts` — Node 内置 `node:test` 验收测试
- `examples/livelock-demo.ts` — 假死定位演示
