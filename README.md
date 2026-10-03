# CAS-Based Token Bucket（原子无锁并发令牌桶 · TypeScript）

消除互斥锁竞争的高性能令牌桶：全部共享状态打包进**一个 64 位原子字**，
通过 `Atomics.compareExchange` 原子地同时更新「最后刷新时间 + 可用令牌数」。
实现中**不存在任何 Mutex / 排他锁**，CAS 失败仅自旋重试，天然无死锁，
基于 `SharedArrayBuffer` 可跨 Worker 线程共享，支持百万级并发安全。

## 设计

```
SharedArrayBuffer(16 bytes)
┌─────────────────────────────────────┬─────────────────────────────┐
│ u64 状态字                           │ f64 创建时间 createdAtMs     │
│ [ lastMs : 34 bit | tokens : 30 bit ]│                             │
└─────────────────────────────────────┴─────────────────────────────┘
```

- **打包原子更新**：`lastMs`（距创建毫秒数）与 `tokensFp`（定点令牌数，
  1 token = 1024 fp）同居一个 64 位字，一次 CAS 同时提交两者，
  杜绝「时间已更新但令牌未扣」之类的中间态脏数据。
- **无锁 CAS 自旋**：`tryAcquire` 读字 → 计算补充量 → 扣减 →
  `Atomics.compareExchange` 提交；冲突则重试。令牌不足时不写回直接失败，
  降低争用。
- **精确守恒**：补充量按 `entitlement(t) = floor(elapsedMs·rate·1024/1000)`
  的差值计算（伸缩求和），任意时刻满足
  `已放行 + 剩余 === 初始 + entitlement(lastMs)`（桶未触顶饱和时严格相等；
  饱和丢弃是令牌桶语义，此时退化为 `≤`）。测试对此做**逐位精确断言**。
- **跨线程**：创建方 `new CASBasedTokenBucket(opts)` 后将 `bucket.buffer`
  传给 Worker，Worker 以 `{ ...opts, buffer }` 附加同一共享状态。

约束：容量 ≤ 1,048,575 token（30 bit 定点字段），创建后约 544 年内有效。

## 运行要求

Node.js ≥ 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，零依赖、零构建）。

```bash
npm test        # Node.js 内置测试运行器（node:test）
npm run demo    # 运行样例
```

## API

| 方法 | 说明 |
| --- | --- |
| `tryAcquire(tokens = 1): boolean` | 无锁尝试获取；不足返回 `false` 且不扣减 |
| `acquire(tokens?, timeoutMs?): Promise<boolean>` | 异步等待获取，超时返回 `false` |
| `availableTokens(): number` | 只读快照：当前可用令牌（含应补充部分） |
| `inspect(): BucketSnapshot` | 只读一致性快照，用于校验守恒恒等式 |

## 运行样例（`npm run demo`）

```
=== 单线程示例：capacity=5, refillRate=10/s ===
  tryAcquire(1) #1 -> true
  ...（#2~#5 true）
  tryAcquire(1) #6 -> false
  tryAcquire(1) #7 -> false
  等待 300ms 让令牌补充...
  availableTokens() -> 3.08
  await acquire(2) -> true

=== 并发压测：100 workers × 500ms, rate=100000/s ===
  墙钟耗时:        3823 ms
  总放行:          382200 token
  理论铸造:        382200 token
  桶内剩余:        0 token
  实际吞吐:        99974 token/s
  守恒校验(放行+剩余===铸造): PASS ✔
```

## 测试结果（`npm test`，Node v23.9.0）

```
✔ 基本获取：扣减令牌，耗尽后拒绝
✔ 获取失败不扣减任何令牌
✔ 随时间补充令牌，且不超过容量上限
✔ 速率整形：单位时间放行量不超过 初始 + 应补充
✔ 异步 acquire：等待补充后成功，超时返回 false
✔ 验收：100 个并发 Worker 高频 Acquire，无锁、无死锁、总量精确守恒
    100 workers × 400ms：总放行 336855 token，理论铸造 337100，
    剩余 245，守恒校验精确通过（336855 + 245 === 337100）
ℹ tests 6  ℹ pass 6  ℹ fail 0
```

验收边界覆盖：100 个 `worker_threads` 并发高频 `tryAcquire()`，全程仅使用
`Atomics` CAS（源码中无任何锁原语），无死锁；以定点精度断言
「总放行 + 剩余 === 理论铸造」，与理论值**完全吻合**。

## 目录结构

```
src/token-bucket.ts   核心实现（CAS 无锁令牌桶）
src/worker.ts         压测 Worker 入口（附加共享状态高频获取）
test/token-bucket.test.ts  Node.js 内置测试（含 100 Worker 验收用例）
examples/demo.ts      运行样例
```
