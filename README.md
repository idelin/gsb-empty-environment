# 分布式滑动窗口限流：心跳租约 + 配额预分配（TypeScript）

中心协调器（Master）持有全局限流池，通过**心跳租约**把配额批量预分配给各工作节点（Worker）。
Worker 本地先消耗预分配的 Local Token，余量低于阈值时**异步批量心跳续订**，
把"每请求一次 RPC"降为"每批一次 RPC"；池子耗尽时 Master 从空闲 Worker **调拨空余额度**给突发节点。

零运行时依赖，测试使用 Node.js 内置 `node:test`。

## 架构

```
            ┌──────────────────────────────┐
            │      MasterCoordinator       │
            │  限流池 3000 QPS / 1s 滑动窗口 │
            │  · 租约发放 / 过期回收        │
            │  · 空闲额度调拨 (reclaim)     │
            │  · 全局不变式:               │
            │    consumed + outstanding    │
            │      ≤ totalQps              │
            └──────────────┬───────────────┘
              heartbeat {used, requested}
        ┌──────────┬───────┴───┬──────────┐
   ┌────▼───┐  ┌───▼────┐  ┌───▼────┐
   │Worker A│  │Worker B│  │Worker C│   本地 Token 桶 + 阈值异步续订
   └────────┘  └────────┘  └────────┘
```

- `src/slidingWindow.ts` — 分桶滑动窗口计数器（Master 统计全窗口消耗与各 Worker 速率）
- `src/master.ts` — 中心协调器：租约发放、过期回收、空闲调拨、全局不变式
- `src/worker.ts` — 工作节点：本地扣减、阈值触发异步批量续订、租约过期作废
- `src/clock.ts` — `SystemClock` / `FakeClock`（测试用确定性时钟）
- `src/demo.ts` — 验收场景演示脚本

## 关键设计

- **批量预分配**：Worker 每次心跳按 `batchSize` 续订一批 token；余量 ≤ 20% 阈值时后台异步触发，
  请求路径不阻塞（`tryAcquire` 纯本地操作）。
- **保活与续订分离**：周期性 keepalive 只上报用量、刷新租约（`requested=0`），
  不请求新额度，避免配额在节点间"乒乓"。
- **空闲调拨**：续订超出池中余量时，Master 按"租约 − 窗口内上报用量"计算各节点空闲额度，
  从最空闲的节点开始回收调拨；活跃节点的配额受保护。
- **租约过期**：TTL（默认 = 窗口长度）内未续订的租约，未消耗额度自动归还池中；
  Worker 侧过期即作废本地余量。
- **重试抑制**：续订颗粒无收（池子真耗尽）时暂停自动续订，等窗口滑过再试，避免 RPC 风暴。
- **惰性回收**：Master 回收某节点空闲额度后，该节点本地已持有的 token 在下一次心跳时
  按响应中的 `allocation` 夹紧（单次心跳窗口内允许极小超发，是预分配方案的常规取舍）。

## 运行

要求 Node.js ≥ 22.6（TypeScript type-stripping；23.6+ 可去掉 `--experimental-strip-types`）。

```bash
nvm use          # 或任何 >= 22.6 的 Node
npm test         # Node 内置测试运行器，14 个用例
npm run demo     # 验收场景演示
```

## 验收场景（npm run demo 实际输出）

3 个 Worker 平分 3000 QPS（各 1000）；B 涓流 200、C 空闲；A 突发流量耗尽自身配额后，
批量心跳从空闲的 C 调拨 1000、再从低负载的 B 调拨 600：

```
=== 阶段 1：初始平分 3000 QPS ===
Worker | 未消耗租约 | 窗口内已用 | 租约剩余TTL(ms)
-------|-----------|-----------|----------------
  A    |      1000 |         0 |           1000
  B    |      1000 |         0 |           1000
  C    |      1000 |         0 |           1000
池子余量 available=0  窗口内已消耗 consumed=0  总量=3000
✓ 全局不变式成立: consumed(0) + outstanding(3000) = 3000 ≤ 3000

=== 阶段 3：Worker A 突发流量 ===
A 本轮突发共放行 2600 个请求，心跳 RPC 仅 4 次
额度调拨记录：
  Master 从空闲的 Worker C 调拨 1000 → Worker A
  Master 从空闲的 Worker B 调拨 600 → Worker A
✓ 全局不变式成立: consumed(2600) + outstanding(400) = 3000 ≤ 3000
A 继续请求被限流拒绝: tryAcquire() = false

=== 阶段 4：休眠 1100ms 等待滑动窗口重置 ===
窗口重置后 A 重新获取配额: acquire() = true

=== 汇总 ===
Worker A: 放行 2601，拒绝 5，心跳 5 次      ← 2601 个请求仅 5 次 RPC
Worker B: 放行 200，拒绝 0，心跳 2 次
Worker C: 放行 0，拒绝 0，心跳 2 次
Master 共处理 heartbeat RPC 9 次
```

## 测试结果（npm test）

```
✔ 验收：A 突发耗尽配额，批量心跳从空闲的 C 调拨额度
✔ Master：池子耗尽后，新请求从持有空闲配额的 worker 回收调拨
✔ Master：活跃 worker 的配额受保护，不可被回收
✔ Master：重复注册与未知 worker 报错
✔ Master：租约过期后未消耗额度自动归还池中
✔ Master：窗口滑过后已消耗额度回收，可重新分配
✔ Master：优先从空闲量最大的 worker 回收调拨
✔ 滑动窗口：统计窗口内的累计值
✔ 滑动窗口：超过窗口后旧数据衰减为 0
✔ 滑动窗口：窗口滑动时逐桶过期
✔ Worker：本地配额内直接放行，不产生额外 RPC
✔ Worker：余量低于阈值时触发异步批量续订
✔ Worker：本地耗尽且续订未到账时拒绝请求
✔ Worker：租约过期后本地余量作废，需重新续订
ℹ tests 14  ℹ pass 14  ℹ fail 0
```

验收用例（`test/acceptance.test.ts`）断言：初始平分 1000/1000/1000；A 突发 ≥ 2000；
调拨记录含 `C→A 1000` 与 `B→A 600`；全程满足 `consumed + outstanding ≤ 3000`；
池子耗尽后 A 被拒绝；窗口滑过后 A 恢复放行。
