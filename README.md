# 单核抢占式调度模拟器

基于离散时间片（tick）的单核抢占调度模拟器，支持：

- **抢占式优先级调度**：每个 tick 选择有效优先级最高的就绪任务运行（优先级数值越大越高）。
- **传递式优先级继承**：任务阻塞在锁上时，把有效优先级捐赠给锁持有者；持有者若也在等锁，
  捐赠沿等待链继续向上传递（不动点迭代收敛）。锁释放后每个 tick 都会重算有效优先级，
  继承来的优先级自动回落到基础优先级。
- **锁等待超时**：`acquire` 可配置 `timeout`，等待者超时退出，策略二选一：
  - `abort`（默认）：任务终止，其持有的锁被强制释放（可能打破死锁）；
  - `continue`：跳过本次 acquire，继续执行后续片段。
- **死锁检测**：在“任务 → 锁持有者”的等待图上找环（每个阻塞任务只有一条出边，
  是函数图，沿指针即可找全所有环）。无可运行任务、无未来释放、无待触发超时时判定为
  **永久死锁**并终止仿真，明确区别于普通空闲（idle）。
- **逐时间片输出**：每个 tick 打印运行任务及其有效优先级/继承来源、就绪队列、
  每个阻塞任务的阻塞原因（等哪把锁、锁被谁持有、已等待时长、剩余超时）与事件；
  纯空闲且未来有事件时合并快进（`t=4..7` 形式），不丢时间片。

## 运行

需要 Node.js ≥ 23.6（原生支持运行 TypeScript）；Node 22.6+ 需加 `--experimental-strip-types`。
如需 `npm run typecheck`，请联网安装 devDependency `typescript`。

```bash
node src/main.ts examples/priority-inheritance.json   # 传递式优先级继承
node src/main.ts examples/deadlock.json               # 死锁（退出码 2）
node src/main.ts examples/timeout.json                # 超时 abort / continue
node src/main.ts examples/basic-preemption.json       # 基本抢占 + 空闲快进
node test/simulator.test.ts                           # 运行测试
```

退出码：`0` 全部完成；`2` 永久死锁；`3` 超过 `maxTicks`；`1` 输入非法。

## 输入格式（JSON）

```jsonc
{
  "name": "场景名",
  "maxTicks": 10000,          // 可选，仿真上限
  "tasks": [
    {
      "id": "T1",
      "priority": 2,           // 基础优先级，越大越高
      "releaseTime": 0,        // 可选，到达时间，默认 0
      "segments": [            // 离散执行片段，按序执行
        { "type": "compute", "duration": 3 },
        { "type": "acquire", "lock": "A", "timeout": 5, "onTimeout": "abort" },
        { "type": "release", "lock": "A" }
      ]
    }
  ]
}
```

## 执行语义

- 每个 tick：先处理任务到达与超时触发，再重算有效优先级、检测等待环，
  然后选出有效优先级最高的就绪任务运行一个 tick（同优先级按到达时间、任务 id 排序）。
- `acquire`/`release` 是瞬时动作，在同一 tick 内连续处理；每个 tick 最多消耗 1 单位
  `compute`；`acquire` 失败则任务阻塞，该 tick 结束。
- 锁被释放时所有等待者变为就绪，被调度到时重试 `acquire`（不保证一定抢到）；
  同一次 `acquire` 的等待时间跨唤醒累计，用于超时判定。
- 任务完成（或 abort）时会强制释放其仍持有的锁并记录事件。
- 对未持有的锁执行 `release`、重复 `acquire` 已持有的锁：记录事件并忽略，不会崩溃。

## 输出示例

```text
[      t=4] | RUN TL eff=3(base 1, inherited from TH) | ready: TM(eff=3(base 2, inherited transitively from TH)) | blocked: TL waits B held by TM, waited 1, no timeout; TH waits A held by TL, waited 3, no timeout | events: TL blocked on lock B held by TM
[      t=6] | STALLED (no runnable task) | ready: - | blocked: T1 waits B held by T2, ... | DEADLOCK CYCLE: T1 -> T2 -> T1 | events: DEADLOCK detected: T1 -> T2 -> T1; permanent deadlock: ...
```

## 代码结构

- `src/types.ts` — 场景输入模型
- `src/simulator.ts` — 仿真核心（调度、传递继承、超时、死锁检测）
- `src/validate.ts` — 场景 JSON 校验
- `src/format.ts` — 逐 tick 时间线与摘要格式化
- `src/main.ts` — CLI 入口
- `examples/` — 四个示例场景；`test/simulator.test.ts` — 断言式测试
