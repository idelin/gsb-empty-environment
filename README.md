# fiber-debugger

可暂停与逐步单步的协程（Fiber）调试器：对 Fiber 调度过程做控制反转，
调度器的每一步推进都由调试器显式触发，便于自动化测试排错。

## 能力

- **断点**：`dbg.setBreakpoint(fiberId, pc)` 在指定协程的指定指令处暂停；
- **暂停全局时钟**：`dbg.pauseClock()` / `resumeClock()`，暂停期间睡眠协程不到期；
- **单协程单指令步进**：`dbg.stepFiber(id)` 只执行该协程的一条指令，不推进时钟、不影响其他协程；
- **逐 Tick 推动**：`dbg.tick()` 手动推进一个时隙（时钟 +1，唤醒到期睡眠者，每个就绪协程执行一条指令）；
- **挂起原因查看**：`dbg.snapshot()` 返回每个协程的状态与挂起原因（等待锁 / 等待 Channel / 睡眠 / 等待资源）；
- **状态迁移字典**：`dbg.transitions()` 记录每个协程在各时隙的状态迁移；
- **假死定位**：`dbg.findStuckFibers(from, to)` 找出区间内无状态迁移且未结束的协程及其卡住的时隙与原因。

## 运行环境

Node.js >= 23.6（原生 TypeScript 类型擦除），测试使用内置 `node:test`，无第三方依赖。

```sh
npm test    # 运行内置测试
npm run demo # 运行假死定位演示
```

## 示例

```ts
import { Debugger, Scheduler, sleep, waitResource, provideResource } from './src/index.ts';

const scheduler = new Scheduler();
const dbg = new Debugger(scheduler); // 单步调试模式：一切推进都需显式触发

scheduler.spawn(function* () {
  yield sleep(1);
  yield waitResource('db-config'); // 等待一个永远未初始化的资源 -> 假死
}, 'worker');

for (let i = 0; i < 6; i += 1) dbg.tick(); // 逐个 Tick 手动推动

console.log(dbg.snapshot());               // 各协程挂起原因
console.log(dbg.transitions().get('worker')); // worker 的状态迁移字典
console.log(dbg.findStuckFibers(2));       // -> worker 自 tick=2 起卡在 waiting-resource
```
