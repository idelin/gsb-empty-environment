# Lease Manager with Clock-Drift Tolerance

单机模拟的分布式租约（Lease）协调内核，TypeScript 实现，Node.js 内置测试。

## 安全模型

- 租约由主节点持有，TTL 明确（示例 10s）。
- 考虑最大时钟漂移 `maxClockDriftMs` 与通信往返 `maxRttMs`，主节点只在
  **安全窗口** `ttl - maxDrift - maxRtt` 内以主身份下发写操作
  （10s TTL + 500ms 漂移 → 9.5s 时停止写）。
- 过期计算全部基于单调时钟，墙钟突变无法延长租约。
- 自检比较墙钟与单调钟的推进差：偏差超过最大漂移（如前拨 1s）即触发
  **紧急提前降级**（emergency demotion），在脑裂发生前完成 fencing。

## 运行

需要 Node.js >= 22.6（使用 `--experimental-strip-types` 直接运行 TS，零依赖）：

```sh
npm test    # node --test 内置测试
npm run demo
```

## 结构

- `src/clock.ts` — `Clock` 抽象、`SystemClock`、可注入突变的 `ManualClock`
- `src/lease.ts` — `LeaseManager`：安全窗口、自检、写 fencing、降级事件
- `test/lease.test.ts` — 8 个用例，覆盖验收边界（9.5s 停写、1s 前拨紧急降级）
- `examples/demo.ts` — 两个场景的运行样例（正常时间线 / 时钟突变）
