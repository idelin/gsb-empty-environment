# llcache — L1/L2 共享版本两级缓存

纯 Go 标准库实现（无任何外部依赖、无远程缓存客户端）。L1（`Node`）与 L2 共享同一套
单调递增的版本号；数据源、时钟、失败注入全部为可注入函数，便于确定性测试。

## 一致性契约（不虚报强一致）

- **Strong 读**：每次都向 L2 重新校验，因此 L2 更新通知延迟不会影响它；L2 不可用时
  返回 `ErrUnavailable`，**绝不**静默降级为旧 L1 命中。
- **Eventual 读**：允许命中 L1，但条目必须通过本地校验——其版本须高于该 key 的
  失效水位（由 L2 失效通知重建）。通知延迟窗口内可能返回旧版本，这是**声明的**行为；
  调用方也可以选择等待延迟结束后收敛。
- **降级规则（L2 故障）**：Eventual 读先回退到数据源（`Degraded=true`）；数据源也不可用
  时返回无法校验的 L1 旧值并显式标记 `Stale=true, Degraded=true`。Strong 读直接失败。
- **版本回退拒绝**：L2 提交小于等于当前版本的写入返回 `ErrVersionRollback`，全局水位
  只增不减；L1 写入同样保持单调。
- **失效幂等**：重复或更旧的失效通知是 no-op（`AppliedInvalidations` 只计实际生效次数）。
- **资源释放**：`Node.Close` / `L2.Close` 停止并等待所有 goroutine（含延迟投递中的通知）。

## 关键文件

- `types.go` — 一致性级别、结果标记、可注入函数类型（`Loader`/`Clock`/`FailHook`）
- `l2.go` — L2：版本提交、全局水位、延迟失效广播、失败注入点
- `node.go` — L1：失效水位校验、强/最终一致读路径、幂等失效应用
- `cache_test.go` — 全部行为测试（`go test`）
- `cmd/demo/main.go` — 可运行样例

## 运行

```console
$ go test ./...
ok  	llcache	1.700s

$ go run ./cmd/demo
== 1. prime L1 from the data source ==
eventual read: "alice@v1" v1 (source=loader)
eventual read: "alice@v1" v1 (source=l1)

== 2. L2 updated; notification delayed 300ms ==
eventual read (declared stale ok): "alice@v1" v1 (source=l1)
strong read (revalidates now):     "alice@v2" v2 (source=l2)

== 3. wait out the notification delay ==
eventual read (converged): "alice@v2" v2 (source=l2)

== 4. version rollback rejected ==
update with older version: llcache: version rollback rejected

== 5. L2 outage: explicit degradation, no fake strong consistency ==
eventual read: "alice@v2" v2 (source=l1 stale=true degraded=true)
strong read:   error=llcache: L2 unavailable, strong consistency cannot be guaranteed

== 6. Close releases resources ==
node.Close() and l2.Close() returned: all goroutines stopped
```

## 测试结果（`go test -race -v ./...`）

```console
=== RUN   TestEventualReadValidatesInvalidation
--- PASS: TestEventualReadValidatesInvalidation (0.01s)
=== RUN   TestNotifyDelayEventualReturnsOldStrongReturnsNew
--- PASS: TestNotifyDelayEventualReturnsOldStrongReturnsNew (0.00s)
=== RUN   TestNotifyDelayWaitedOut
--- PASS: TestNotifyDelayWaitedOut (0.05s)
=== RUN   TestL2FailureDegradation
--- PASS: TestL2FailureDegradation (0.01s)
=== RUN   TestL2FailureFallsBackToLoader
--- PASS: TestL2FailureFallsBackToLoader (0.00s)
=== RUN   TestInvalidationIdempotent
--- PASS: TestInvalidationIdempotent (0.00s)
=== RUN   TestVersionRollbackRejected
--- PASS: TestVersionRollbackRejected (0.00s)
=== RUN   TestUpdateFailureAtomic
--- PASS: TestUpdateFailureAtomic (0.02s)
=== RUN   TestClockInjectable
--- PASS: TestClockInjectable (0.00s)
=== RUN   TestCloseReleasesGoroutines
--- PASS: TestCloseReleasesGoroutines (0.00s)
PASS
ok  	llcache	1.700s
```
