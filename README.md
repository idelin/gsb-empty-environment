# CAS-Based Token Bucket (Go)

原子无锁并发令牌桶：全程零 `sync.Mutex`，状态更新仅靠一次 64 位 CAS。

## 设计

桶的全部状态打包进单个 `atomic.Uint64`，用 Compare-And-Swap 原子地
**同时**更新「最后刷新时间」和「可用令牌数」，杜绝脏数据与撕裂状态：

```
高 32 位: 上次刷新时间（自创建起的毫秒数，uint32 回绕安全）
低 32 位: 可用令牌数（16.16 定点数，最大 65535 个，精度 1/65536）
```

- `TryAcquire()`：非阻塞取 1 个令牌；CAS 失败仅重试，无锁竞争。
- `Acquire()`：阻塞至有令牌为止（按缺口精确睡眠后重试）。
- `Tokens()`：只读快照，不修改状态。
- 时钟与睡眠函数可注入，测试用假时钟做到**确定性精确断言**。

限制：容量 ≤ 65535 个令牌；单次时间间隔 < ~24.8 天（uint32 毫秒回绕安全）。

## 运行

```sh
go test -race -v ./...   # 测试（含 100 并发 Worker 精确总量验证）
go run ./cmd/demo        # 运行样例：100 Worker 高频压测 1 秒
```

## 验收要点

- `TestConcurrentExactTotal`：100 个 Worker 高频 `TryAcquire`，假时钟下
  总放行数量与理论值**完全相等**（150 == 容量 100 + 补充 50）。
- `TestConcurrentRealClockBounded` / `TestConcurrentBlockingAcquire`：
  真实时钟下放行量落在理论包络内，令牌数永不出界。
- 全部测试在 `-race` 下通过；实现中不存在任何 `Mutex`。
