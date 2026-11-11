# 不可变标签发布事务（package netsec）

制品标签一旦标为不可变，就只能绑定一个上传摘要。两个**同时首次发布**的请求不能各自报告成功：
所有状态迁移都在 `Store.Update` 的单标签串行事务内完成，包括发布前的内容再验证，因此失败不会留下部分写入，
并发竞争者只能有一个赢家，另一个收到 `ErrConflict`。

## 标签状态机

| 状态 | 含义 |
| --- | --- |
| `StateAbsent`（不存在） | 无记录，可直接保留或一次性发布 |
| `StateReserved`（保留） | 请求持有标签，记录绑定**上传摘要 + 请求 ID + 过期时间** |
| `StatePublished`（已发布） | 标签与摘要永久绑定，不可再迁移 |
| `StateRevoked`（撤销保留） | 保留被释放或过期，新请求 ID 可重新获取 |

迁移由服务层与 `MemoryStore.validateTransition` 双重守卫：已发布记录永远不能回到保留/撤销。

## 宿主 API

- `Reserve(ctx, label, digest, requestID)`：上传前保留标签。
- `Publish(ctx, label, digest, requestID)`：事务内重新验证上传内容并发布；从未保留的标签会在同一事务里保留并发布。
- `Release(ctx, label, requestID)`：放弃保留（已发布标签不可释放）。

返回的 `Result.Replayed` 标识该次调用是否为同请求 ID 的幂等回放（记录与版本号不变）。

## 幂等与冲突规则

- 同 `requestID` + 同摘要重试：回放已记录结果，不产生新版本；`Publish` 回放时**仍重新验证内容**。
- 同 `requestID` + 不同摘要：`ErrConflict`（防止重试偷换制品）。
- 不同 `requestID` 竞争活跃保留或已发布标签：`ErrConflict`。
- 保留过期后，原请求 ID 得到 `ErrReservationExpired`；新请求 ID 可在同一事务内原子地撤销并接管。
- 内容未到返回 `ErrContentNotFound`，上传内容与摘要不符返回 `ErrContentMismatch`，事务整体回滚。

## 存储与故障注入

- `Store` 接口要求可串行化事务；`MemoryStore` 按标签加锁，回调只接触深拷贝，提交时自增 `Version`。
- `MemoryStore.setFailHook`（测试辅助）在提交前注入故障，验证“事务中止 = 无任何写入”。

关键故障测试（`*_test.go`）：

- `TestReservationExpiryAndTakeover` / `TestExpiredReservationPublishesAtomicallyForNewRequest`：保留过期与原子接管。
- `TestContentArrivesLate`：内容后到时发布中止且不落库；保留在 TTL 内存活，过期后才能被新请求接管。
- `TestCommitThenResponseLost`：提交成功但响应丢失，同 ID 重试只得到一次幂等回放、不产生二次提交。
- `TestStoreFaultAbortsTransactionAndIdempotentRetryRecovers`：存储故障期间无部分写入，恢复后同 ID 重试恰好发布一次。
- `TestConcurrentFirstPublishesOnlyOneSucceeds`：12 轮带屏障的并发首发布，赢家恒为 1（`-race` 下运行）。

## 快速开始

```go
store := netsec.NewMemoryStore()
svc := netsec.NewService(store, verifier) // verifier 实现 netsec.Verifier

if _, err := svc.Reserve(ctx, "repo/img:v1", "sha256:abc", "request-7"); err != nil {
    log.Fatal(err)
}
// 上传完成后发布；发布前会重新校验上传内容与摘要。
res, err := svc.Publish(ctx, "repo/img:v1", "sha256:abc", "request-7")
```

运行测试：

```sh
go test -race -count=1 ./...
```
