# 单核抢占式调度模拟器

TypeScript 实现的离散时间片（tick）调度模拟器，支持：

- **抢占式调度**：每个 tick 重新选择有效优先级最高的就绪任务（数值越大优先级越高）。
- **传递式优先级继承**：锁持有者继承等待者的有效优先级，沿等待链逐级传递
  （对 wait-for 图做不动点计算，链长任意、有环也不会死循环）。
- **释放锁即重算**：任务释放锁的同一 tick 内重新计算有效优先级并重新调度，
  高优先级等待者可立即抢占持锁者。
- **锁等待超时**：`lockTimeout` 到期后等待者以 `timed_out` 状态退出。
- **逐时间片输出**：每个 tick 显示运行任务（base/eff 优先级）、事件流、
  阻塞任务及其原因（等哪把锁、被谁持有、已等待时长/超时上限）。
- **死锁检测**：对 wait-for 图找环，识别锁等待环并标记 `DEADLOCK`，
  与普通的 `IDLE`（无任务可运行）严格区分；死锁任务以 `deadlocked` 状态终止。

## 构建与运行

```sh
npm install        # 安装 typescript（或任意可用 tsc）
npm run build      # 编译到 dist/
node dist/main.js                  # 运行 4 个内置演示场景
node dist/main.js scenarios/deadlock.json   # 运行 JSON 场景文件
```

## 场景格式

```json
{
  "name": "demo",
  "maxTicks": 10000,
  "tasks": [
    {
      "id": "H",
      "priority": 9,
      "releaseTime": 2,
      "lockTimeout": 3,
      "segments": [ { "lock": "A" }, { "run": 1 }, { "unlock": "A" } ]
    }
  ]
}
```

- `segments` 为离散执行片段：`{"run": N}` 占用 N 个时间片；
  `{"lock": "L"}` / `{"unlock": "L"}` 为瞬时操作（不消耗时间片）。
- `lockTimeout` 省略表示无限等待。
- 任务终止（done / timed_out / deadlocked）时仍持有的锁会被强制释放并输出 warning。

## 输出示例

```
t=002 | RUN L (base=1, eff=9) | blocked: H->A(held by L, waited 0/3)
       ! H blocks on A (held by L, holder eff priority 1)
t=003 | DEADLOCK (no runnable task)
       ! DEADLOCK: lock wait cycle detected: A -> B -> A
```

CPU 列取值：`RUN <任务>` / `IDLE`（无就绪也无阻塞）/
`STALLED`（有阻塞但无可运行）/ `DEADLOCK`（检测到等待环）。

## 代码结构

- `src/types.ts` — 场景、时间片记录等类型定义
- `src/scheduler.ts` — 调度核心：有效优先级不动点计算、瞬时片段处理、
  超时检查、wait-for 环检测
- `src/main.ts` — CLI、JSON 场景解析、内置演示场景、输出渲染
- `scenarios/` — 示例 JSON 场景
