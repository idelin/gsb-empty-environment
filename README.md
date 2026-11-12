# DNS委派切换并发查询

解析器更换权威服务器集时，在途旧查询的答案不能覆盖新委派缓存。实现供Go解析宿主调用的查询计划包，每次查询固定委派代次，追踪各候选结果与超时，旧代回答只能完成原请求不能更新新代缓存。用可暂停上游测试全旧代迟到、部分新代失败和取消后无悬挂等待者。

## 包结构：`queryplan`

- `Planner` 持有当前委派代次与按代次隔离的答案缓存，供解析宿主并发调用 `Lookup`。
- 每次 `Lookup` 在入口快照委派并固定 `Generation`；计划内每个候选运行在独立 goroutine，带每候选超时，状态记录为 `success/timeout/canceled/failed`，首个成功答案完成请求，其余候选被取消并在返回前排空。
- 缓存写入与 `NewDelegation` 引用安装都以“回答代次 == 当前代次”为门控；委派切换后，旧代在途答案仍可完成原请求，但不会写入新代缓存或回滚代次。
- `ReplaceDelegation` 拒绝相等或更旧的代次，避免陈旧引用倒退时钟。

### 测试

测试夹具 `pausableUpstream` 可按代次或候选名暂停上游调用并统一释放，不依赖真实 I/O 时序：

- `TestAllOldGenerationAnswersArriveLate`：全部旧代答案迟到，验证原请求完成且新代缓存不被覆盖。
- `TestPartialNewGenerationFailure`：部分候选超时/失败/成功的状态追踪，以及全失败时不写缓存。
- `TestCancelLeavesNoDanglingWaiters`：取消后无阻塞上游调用、无残留计划 goroutine 或等待者。

运行：

```
go test -race ./...
```
