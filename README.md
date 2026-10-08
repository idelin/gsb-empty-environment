# idem — 幂等请求执行库

按幂等 id 缓存执行结果，保证业务效果**恰一次**落库。

## 语义

- **原子关联**：`fn` 是纯函数，返回 `Effect` 与结果；二者在 `Store.Commit`
  的同一个临界区内持久化。崩溃不可能留下"有效果无结果"或"有结果无效果"
  的撕裂状态。提交成功但回执丢失（崩溃在写后、响应前）时，重试 `Do` 直接
  返回已持久化的结果，绝不重新执行。
- **参数指纹**：同 id 不同参数拒绝（`ErrFingerprintMismatch`），指纹在
  墓碑阶段仍保留，过期后依然能识别参数冲突。
- **墓碑**：结果 TTL 到期后经 `Sweep` 转为墓碑，丢弃结果、保留指纹；
  重放返回 `ErrExpired`，永不重新执行。
- **租约 + 围栏令牌**：执行者崩溃后，租约到期可被接管；持旧令牌的
  过期执行者提交被拒绝（`ErrLostClaim`），防止陈旧效果落库。

## 持久化切点枚举验证

`MemoryStore` 的每个持久化写都是一个**切点**，注入器可武装第 n 个切点，
并选择崩溃在写入**之前**（写丢失）或**之后**（写已持久化、回执丢失）。
测试枚举全部切点 × 两种崩溃时机：

- `TestExactlyOnce_CutPointEnumeration`：单次执行的每个切点崩溃后重启，
  效果恰一次、结果可恢复。
- `TestLifecycle_CutPointEnumeration`：执行→过期→清理全生命周期枚举，
  验证墓碑拒绝重放且效果恰一次。
- `TestConcurrentDuplicates`：64 并发重复（半数参数不同），恰一次执行、
  同参数收敛到缓存结果、异参数拒绝。
- `TestExpiryTombstone` / `TestLostClaimCannotCommit`：清理路径与围栏。

```sh
go test -race ./...
```
