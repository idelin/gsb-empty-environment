# leasepool — 租约配额池与弹性借贷还款管理器

分布式并发任务许可证配额租约池，纯 Go 标准库实现。

## 能力

- 固定总配额（默认 100）的租约池，业务线按需申请指定数量配额并承诺借期（TTL）
- 协调器后台巡检，超期未归还的租约被强制吊销（Revoke）并回收配额
- 高优先级申请插队：等待队列中高优请求优先获得释放的配额，并触发对超期租约的抢占式回收
- 全程审计日志（GRANT / RETURN / REVOKE / PENALTY），违约方按吊销配额数累计惩罚积分

## API 概览

```go
pool := leasepool.NewPool(100, 100*time.Millisecond) // 总配额 + 巡检周期
defer pool.Close()

lease, err := pool.Acquire(ctx, "business-A", 80, 10*time.Second, leasepool.PriorityHigh)
err = pool.Return(lease)          // 主动归还（还款）
pool.Available()                  // 当前空闲配额
pool.Penalty("business-A")        // 违约惩罚积分
pool.AuditLog()                   // 完整审计日志
```

## 运行测试

```sh
go test ./... -v
```

## 运行验收演示

```sh
go run ./cmd/demo
```

场景：业务 A 租赁 80 个配额并在 10 秒后到期未归还；高优业务 B 此时申请 50 个配额；
协调器强行吊销 A 的超期租约并将配额转借给 B，输出配额违约惩罚审计日志。

实测输出：

```
=== 租约配额池演示：总配额 100 ===
[t=  0.0s] 业务 A 租得 80 个配额 (lease #1)，承诺 10s 后归还，当前空闲 20
[t= 10.5s] 业务 A 已超期未归还！当前空闲配额 100
[t= 10.5s] 高优业务 B 租得 50 个配额 (lease #2)，等待耗时 0s，当前空闲 50
           业务 A 的租约状态: revoked=true active=false

=== 配额审计日志 ===
[22:33:20.995] GRANT   owner=business-A   lease=#1   count=80  ttl=10s priority=1 available=20
[22:33:31.004] REVOKE  owner=business-A   lease=#1   count=80  overdue=0s force-revoked, quota reclaimed (available=100)
[22:33:31.004] PENALTY owner=business-A   lease=#1   count=80  violation penalty +80, total=80
[22:33:31.522] GRANT   owner=business-B   lease=#2   count=50  ttl=30s priority=2 available=50

=== 违约惩罚积分 ===
business-A   penalty=80
business-B   penalty=0
```

## 测试结果

```
=== RUN   TestBasicAcquireAndReturn
--- PASS: TestBasicAcquireAndReturn (0.00s)
=== RUN   TestAcquireExceedsTotal
--- PASS: TestAcquireExceedsTotal (0.00s)
=== RUN   TestBlockingAcquireUntilReturn
--- PASS: TestBlockingAcquireUntilReturn (0.10s)
=== RUN   TestAcceptanceRevokeOverdueAndPreempt
--- PASS: TestAcceptanceRevokeOverdueAndPreempt (0.40s)
=== RUN   TestSweeperRevokesOverdue
--- PASS: TestSweeperRevokesOverdue (0.11s)
=== RUN   TestPriorityPreemption
--- PASS: TestPriorityPreemption (0.36s)
=== RUN   TestAcquireContextTimeout
--- PASS: TestAcquireContextTimeout (0.10s)
PASS
ok      leasepool
```

`go test -race ./...` 亦通过。
