# CAS-Based Token Bucket (TypeScript)

原子无锁并发令牌桶: 消除互斥锁竞争的高性能实现。基于 `SharedArrayBuffer` +
`Atomics.compareExchange` (硬件级 CAS), 把**最后刷新时间**与**可用令牌数**打包进
同一个 64 位原子变量一次性提交, 支持百万级并发安全, 全程没有任何 Mutex / Lock。

## 设计要点

- **单原子打包状态**: 高 32 位 = 最后刷新毫秒数, 低 32 位 = 可用令牌定点数
  (SCALE=1024, 即 10 bit 小数, 容量上限约 419 万令牌)。一次 CAS 同时更新两个字段,
  不存在"时间已更新但令牌没更新"之类的中间态。
- **无锁 CAS 循环**: `tryAcquire()` 读快照 → 本地结算补给 → `Atomics.compareExchange`
  提交; 失败说明被竞争者抢先, 重读重试。无互斥锁 ⇒ 无死锁、无优先级反转。
- **零累计舍入误差**: 补给量是"绝对时间的确定函数"
  `refill(t) = floor(t · rate · SCALE / 1000)`, 多次刷新/重试之间不累积误差,
  因此总量可以被**精确**对账, 而不是"约等于"。
- **跨线程共享**: 状态放在 `SharedArrayBuffer` 里, 主线程创建后把 buffer 分发给
  任意数量的 `worker_threads`, 所有 Worker 操作同一个桶。

## 精确不变量 (验收依据)

任意时刻 (无容量钳制发生时) 满足定点整数恒等式:

```
已放行令牌 × SCALE + 桶内剩余(scaled) == 初始令牌 × SCALE + refill(当前毫秒)
```

并发测试在所有 Worker 退出后结算到同一时刻, 验证两边**完全相等**。

## 运行要求

Node.js ≥ 22.6 (利用原生 TypeScript 类型擦除直接运行 `.ts`, 无需编译步骤;
开发机上用 v23.9.0 验证)。如需类型检查可自选 `npm i -D typescript @types/node && npx tsc`。

## 运行样例

```bash
nvm use 23        # 或任意 ≥22.6 的 Node
npm run demo      # 100 个 Worker 高频争抢 1 秒, 输出对账结果
npm test          # node:test 内置测试, 5 个用例
```

`npm run demo` 实际输出:

```
启动 100 个 Worker, 高频调用 tryAcquire(), 持续 1000ms ...
桶参数: capacity=4000000, rate=64000/s, initial=1000
全部 Worker 就绪, 开始压测 ...

总放行令牌:        244150
Worker 上报合计:   244150 (与共享计数器一致: true)
桶内剩余(scaled):  51103744
理论总量(scaled):  301113344
实际总量(scaled):  301113344
对账结果:          完全吻合 ✓ (无死锁、无脏数据、无互斥锁)
```

## 测试结果

`npm test` (Node v23.9.0, `node --test`) 实际输出:

```
✔ 确定性补给: 按虚拟时钟精确累积与扣减 (20.458875ms)
✔ 容量钳制: 补给不会超过 capacity (0.22275ms)
✔ 定点精度: 亚毫秒级速率无累计舍入误差 (0.075666ms)
✔ 不变量: 已放行 + 剩余 == 初始 + 累计补给 (单线程) (9.578625ms)
    [并发验收] workers=100 放行=460386 剩余=106054 理论=580034560 实际=580034560 完全吻合
✔ 验收: 100 个并发 Worker 高频争抢, 无锁无死锁, 总量与理论值完全吻合 (8835.7875ms)
ℹ tests 5
ℹ pass 5
ℹ fail 0
```

验收边界覆盖:

- **100 个并发 Worker 高频调用**: `worker_threads` × 100, 紧循环 `tryAcquire(1)` 2 秒。
- **完全无互斥锁**: 实现只使用 `Atomics.compareExchange / load / store / add`,
  源码中不存在任何 Mutex/Lock。
- **无死锁**: 全部 Worker 在测试超时前正常退出 (exit code 0)。
- **无脏数据**: 两条独立计数路径 (Worker 逐条上报合计 vs 共享原子计数器) 完全一致。
- **总量与理论值完全吻合**: 定点整数恒等式精确相等 (580034560 == 580034560)。

## 文件结构

```
src/token-bucket.ts   无锁令牌桶核心 (CAS 状态机)
src/worker.ts         压测 Worker 入口
test/token-bucket.test.ts  node:test 测试 (含 100 Worker 并发验收)
examples/demo.ts      运行样例
```
