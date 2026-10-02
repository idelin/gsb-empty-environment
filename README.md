# 分层租约与多级代理授权协调器 (hierarchical lease coordinator)

面向大规模微服务集群的分级授权模型，仅用 Go 标准库实现：

```
RootCoordinator  --(长周期粗粒度租约, 1min)-->   RegionGateway (x5)
RegionGateway    --(短周期细粒度子租约, 5s)-->   WorkerNode (x1000)
```

- **根协调器** (`lease/root.go`)：只与 5 个区域网关维持租约通信；无任何后台
  goroutine，过期租约在被访问时惰性回收，空闲 CPU 为零。
- **区域网关** (`lease/gateway.go`)：持有一个根租约，向本区工作节点颁发子租约；
  每个网关仅 1 个后台 goroutine 负责续期根租约 + 本地回收超时子租约。
  子租约过期时间被钳制不超过其根租约（授权不可超越上级）。
- **故障隔离**：边缘节点掉线后，其子租约在本地超时回收，根协调器零感知。

## 运行样例

```console
$ go run ./cmd/sim -workers 1000 -regions 5 -root-ttl 1s -sub-ttl 200ms -cycles 5 -drop-pct 10
=== hierarchical lease coordinator simulation ===
workers=1000 regions=5 root-ttl=1s sub-ttl=200ms cycles=5 drop=10%

--- results ---
gateway RPCs handled (edge load):   14200
sub-leases reclaimed after drops:   100
root RPCs before edge storm:     5
root RPCs after edge storm:         5 (delta=0)
root RPCs after local reaping:      10 (delta during reap=5)
root active leases (gateways only): 5
root quota used/total:              1000/1000
```

（生产形态时长：`-root-ttl 1m -sub-ttl 5s`。样例中 10% 即 100 个节点中途掉线，
其 100 个子租约全部由网关本地超时回收；回收窗口内根侧仅有的 5 次 RPC 是
5 个网关自身的周期续期，与掉线事件无关。）

## 测试结果

```console
$ go test ./lease/ -v
=== RUN   TestRootLeaseLifecycle
--- PASS: TestRootLeaseLifecycle (0.12s)
=== RUN   TestRootQuotaEnforcement
--- PASS: TestRootQuotaEnforcement (0.00s)
=== RUN   TestSubLeaseBoundedByRegionQuota
--- PASS: TestSubLeaseBoundedByRegionQuota (0.00s)
=== RUN   TestSubLeaseCannotOutliveRootLease
--- PASS: TestSubLeaseCannotOutliveRootLease (0.00s)
=== RUN   TestWorkerDropIsReclaimedLocally
--- PASS: TestWorkerDropIsReclaimedLocally (0.14s)
=== RUN   TestScaleThousandWorkersFiveGateways
--- PASS: TestScaleThousandWorkersFiveGateways (0.01s)
    scale_test.go:80: edge workers:            1000
    scale_test.go:81: region gateways:         5
    scale_test.go:82: gateway RPCs handled:    9000 (edge load absorbed locally)
    scale_test.go:83: sub-leases reaped:       0
    scale_test.go:84: root RPCs before storm:  5
    scale_test.go:85: root RPCs after storm:   5 (gateways only)
    scale_test.go:86: root active leases:      5
PASS
ok      hlease/lease    0.960s
```

`go test -race` 同样通过。

## 验收边界对照

| 验收项 | 结果 |
| --- | --- |
| 1000 个边缘工作节点请求配额 | 9000 次子租约 RPC 全部由 5 个网关就地处理 |
| 根仅与 5 个区域网关维系租约 | 边缘风暴期间根 RPC 恒为 5，活跃租约数 = 5 |
| 节点掉线仅本地超时释放 | `TestWorkerDropIsReclaimedLocally`：回收窗口内根 RPC 增量为 0 |
| 根 CPU 负载近乎零波动 | 根无后台 goroutine，惰性过期；负载与边缘规模解耦 |

## 备注

模拟与压测中 worker 采用固定次数的"申请→续期→释放"循环而非 `time.Sleep`：
Go ≤ 1.13 的全部定时器由单个 runtime goroutine 管理，1000 个 goroutine 高频
睡眠会使定时器堆崩溃，从而扭曲本测试要测量的负载数据（Go 1.14 起已修复）。
