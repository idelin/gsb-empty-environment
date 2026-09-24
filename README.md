# prefetch

基于显式访问序列的键预测预取库。纯 Go 标准库，无远程缓存依赖；
数据源、时钟、失败全部以可注入函数提供。

## 设计

- `Source` / `SourceFunc`：可注入数据源，返回带单调版本的 `Value`。
- `Clock` / `ManualClock`：可注入时钟，预取段 TTL 由此判定。
- `Cache`：分段缓存。前台段与预取段各自限额，误预测只淘汰预取段，
  不污染全部容量；`Invalidate` 记录版本水位，`Publish` 校验版本，
  过期预取结果无法复活已失效的值。
- `Scheduler`：每个键至多一个加载任务。真实请求 `Get` 命中在途预取时
  原地提升优先级（保留任务身份，不发重复加载）；预取只能使用
  `maxTotal-fgReserve` 个并发槽，前台保留槽保证预取永不饿死前台。
- 代际取消：`AdvanceGeneration` 仅移除旧代际的预取订阅——无真实等待者
  的任务被取消（挂起的出队、运行中的 ctx 取消且结果被丢弃），真实
  等待者不受影响继续完成。
- `Engine`：记录显式访问序列，每次访问推进代际并向多个 `Predictor`
  征集预测；所有预测器共享每轮预算与调度器的预取并发额度。

## 运行

```sh
go test ./...      # 全部测试（含 -race 通过）
go run ./cmd/demo  # 运行样例
```

## 测试覆盖

- `TestSameKeyMergePromotionIdentity`：同键预取与前台合并，提升保留任务身份，仅一次加载。
- `TestPrefetchDoesNotStarveForeground`：预取占满配额时前台立即获得保留槽。
- `TestGenerationCancelKeepsRealWaiters`：代际取消只移除预取订阅，真实等待者继续。
- `TestStaleResultRejectedOnPublish`：失效后旧版本预取结果被拒绝，不复活失效值。
- `TestMispredictionsDontPolluteForeground`：误预测不占用前台容量。
- `TestResourceReleaseOnCancel`：取消后 ctx 传播、槽位释放、任务表清空、Close 返回。
- `TestFailureNotCached`：注入失败不缓存、不毒化键，后续可重试。
- `TestPrefetchTTLExpiry`：预取条目按注入时钟过期。
- `TestSharedBudgetAcrossPredictors`：多预测器共享每轮预算。
- `TestBoundedScheduleExploration`：有限调度探索——枚举受控完成顺序，
  逐步验证并发额度与无饥饿不变量。
