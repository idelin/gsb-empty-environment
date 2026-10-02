# 分布式租约管理器与时钟漂移容忍器

单机模拟的分布式租约（Lease）协调内核：租约由主节点持有并具有明确 TTL；
安全写窗口显式扣除最大时钟漂移与通信往返延迟；主节点在安全期过去后主动放弃特权；
注入超限时钟突变时，安全自检触发紧急提前降级，配合 fencing token 杜绝双主脑裂。

## 环境要求

- Node.js ≥ 23.6（原生 TypeScript 类型擦除，无需编译）；Node 22.6+ 可加 `--experimental-strip-types` 运行。
- 无第三方依赖，测试使用 Node 内置 `node:test`。

## 安全模型

```
安全写窗口 = 租约 TTL − 最大时钟漂移 − 最大 RTT
           = 10_000ms − 500ms − 0ms = 9_500ms
```

- 主节点仅在 `elapsed < 9_500ms` 时允许以主身份下发写操作；到达边界即自动降级（`safe-window-expired`）。
- `ClockDriftMonitor` 对比墙上时钟与单调时钟：若观测到的时钟阶跃 `|step| > maxClockDrift`，
  说明漂移上界假设被破坏，立即触发紧急提前降级（`clock-drift-exceeded`），不再信任剩余安全窗口。
- 每次 `acquire()` 从共享 `FencingTokenIssuer` 领取单调递增的 fencing token；
  `FencedStore` 拒绝携带过期 token 的写入，防止被降级的旧主继续写（脑裂防护）。

## 模块

- `src/clock.ts` — 时钟抽象：`SystemClock` / `MonotonicClock` / `ManualClock`（测试用）/ `AdjustableClock`（可注入突变）。
- `src/drift-monitor.ts` — `ClockDriftMonitor`：采样墙上时钟 vs 单调时钟，检测超限阶跃并通知监听者。
- `src/lease-manager.ts` — `LeaseManager`：获取/续期/写门禁/安全自检/降级事件；`FencingTokenIssuer`。
- `src/fenced-store.ts` — `FencedStore`：按 fencing token 拒绝旧主写入的模拟存储层。

## 运行

```bash
npm test    # node --test test/*.test.ts
npm run demo
```

## 运行样例（`npm run demo`）

```
=== 分布式租约管理器 + 时钟漂移容忍器：验收场景演示 ===
配置: 租约 TTL=10000ms, 最大时钟漂移=500ms, RTT 余量=0ms
=> 安全写窗口 = 10000 - 500 - 0 = 9500ms

[场景 1] 正常运行：安全期（9.5s）过后主节点主动放弃特权
t=    0ms  [node-A] acquire() -> role=primary, fencing token=1
t= 9000ms  [node-A] write("op-1") 允许  (fencing token=1, 租约已持有 9000ms)
t= 9499ms  [node-A] write("op-2") 允许  (fencing token=1, 租约已持有 9499ms)
           [node-A] >>> 降级事件: reason=safe-window-expired, token=1
t= 9500ms  [node-A] write("op-3") 拒绝  (role=demoted, reason=safe-window-expired)

[场景 2] 注入时钟前拨 1 秒突变：安全自检触发紧急提前降级
t=    0ms  [node-B] acquire() -> role=primary, fencing token=1
t= 8000ms  [node-B] write("op-a") 允许  (fencing token=1, 租约已持有 8000ms)
t= 8000ms  [node-B] !! 注入时钟突变: 前拨 +1000ms（超过 500ms 漂移上限）
           [node-B] >>> 紧急降级事件: reason=clock-drift-exceeded, token=1
           [node-B] 安全自检: 观测时钟阶跃 1000ms > 容忍 500ms -> 漂移假设被破坏
t= 8000ms  [node-B] write("op-b") 拒绝  (role=demoted, reason=clock-drift-exceeded)

[场景 3] 脑裂防护：fencing token 让存储层拒绝旧主的写
t=    0ms  [node-A] 持 token=1 写入 balance=100 -> 存储接受
t= 8000ms  [node-A] !! 时钟突变 +1000ms，node-A 被紧急降级（但假设它仍试图写）
t= 8000ms  [node-B] 新主持 token=2 写入 balance=200 -> 存储接受
t= 8000ms  [node-A] 旧主用 token=1 写入 balance=999 -> 存储拒绝: stale fencing token: received 1, but a write with token 2 was already accepted
           最终 balance=200（旧主的脏写被拦截，无双主）
```

## 测试结果（`npm test`，12 通过 / 0 失败）

```
✔ 正常走时：观测阶跃为 0，判定安全
✔ 前拨 1 秒超出 500ms 容忍：判定不安全并通知监听者
✔ 容忍边界：恰好 500ms 的阶跃判定安全，501ms 判定不安全
✔ 后拨突变同样被检测（绝对值判定）
✔ 安全写窗口 = TTL - 最大漂移 - RTT 余量
✔ 主节点在 9.5 秒边界必须停止以主身份下发写操作
✔ RTT 余量使主节点提前停止写入
✔ 注入时钟前拨 1 秒突变：安全自检触发紧急提前降级
✔ 漂移容忍内的小幅时钟调整不触发降级，但安全窗口相应收缩
✔ 租约续期重置安全窗口，且只能在安全期内续期
✔ 降级后重新获取租约会发放递增的 fencing token
✔ fencing token 杜绝双主脑裂：旧主的写被存储层拒绝
ℹ tests 12  ℹ pass 12  ℹ fail 0
```
