# leasecoord — 分层租约与多级代理授权协调器

大规模微服务集群的分级授权模型（Go 标准库实现，无第三方依赖）：

- **根协调器（RootCoordinator）**：向 5 个区域网关颁发长周期（1 分钟）粗粒度租约，按块（chunk）分配集群总配额。
- **区域网关（RegionGateway）**：持有根租约，向本区工作节点颁发短周期（5 秒）细粒度子租约；配额不足时按块向根协调器一次性补足，是屏蔽边缘 churn 的代理授权层。
- **工作节点（WorkerNode）**：只与本区网关通信，获取/续期/释放子租约，从不接触根协调器。

## 验收边界

| 边界 | 机制 | 验证 |
| --- | --- | --- |
| 1000 个边缘节点请求配额 | 5 网关 × 200 节点并发获取子租约 | `TestThousandWorkersRootOnlyTalksToFiveGateways` |
| 根仅与 5 个网关维系租约通信 | 网关按 chunk 批量补足，`root.MessageCount()` 精确等于 5 | 同上 + `TestConcurrentWorkers` |
| 边缘掉线仅本地超时释放，根负载零波动 | 网关本地惰性/定时清扫过期子租约，回收配额不触碰根 | `TestWorkerDropReclaimsQuotaLocally` |

## 运行

```sh
go test ./... -v   # 全部测试（手动时钟，确定性）
go run ./cmd/demo  # 真实时钟样例：1000 节点集群 + 节点掉线模拟
```

## 测试结果

```
--- PASS: TestThousandWorkersRootOnlyTalksToFiveGateways (0.00s)
--- PASS: TestWorkerDropReclaimsQuotaLocally (0.00s)
--- PASS: TestCoarseLeaseExpiryAndReacquire (0.00s)
--- PASS: TestSubLeaseRenewal (0.00s)
--- PASS: TestRootQuotaExhaustion (0.00s)
--- PASS: TestConcurrentWorkers (0.00s)
PASS
ok  	leasecoord	0.104s
```

## 样例输出（go run ./cmd/demo）

```
cluster: 1 root, 5 gateways, 1000 workers (coarse TTL 1m0s, sub TTL 5s)
phase 1: all workers leased; root lease RPCs served = 5 (gateways only)
phase 2: worker-0-0 dropped offline; waiting for its 5s sub-lease to time out...
         gateway-0 live sub-leases = 0 (dropped worker's quota reclaimed locally)
         root lease RPCs during drop: 5 -> 5 (delta = 0, zero central load)
         root outstanding coarse quota = 25000
```

## 代码结构

- `lease.go` — `Lease` 与 `RootCoordinator`（粗粒度租约、配额上限、惰性过期清扫、RPC 计数负载指标）
- `gateway.go` — `RegionGateway`（子租约签发/续期/清扫、按块向根补足配额）
- `worker.go` — `WorkerNode`（获取/续期/释放子租约）
- `clock.go` — `Clock` 抽象与 `ManualClock`（测试用确定性时钟）
- `lease_test.go` — 验收测试
- `cmd/demo/main.go` — 运行样例
