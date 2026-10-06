# seq — 持久化号段序号库

通过一次性预留整段序号来摊薄刷盘成本：每个号段只对存储做一次原子
比较更新（CAS），段内发号纯内存完成。

## 保证

- **绝不复用**：已返回的序号在任何情况下（含崩溃重启）不会被再次返回。
- **允许跳号**：重启后从持久化高水位之上继续，未发完的号段整体跳过。
- **刷盘失败不发号**：预留无法持久化时 `Next` 返回错误，不发任何序号。
- **跨进程协调**：多进程共享同一 `Store`，通过 CAS 预留互斥号段。

## 接口

```go
type Store interface {
    MaxReserved(ctx, key) (uint64, error)
    CompareAndSwapMaxReserved(ctx, key, expected, next uint64) error
}
```

`Store` 是唯一的持久化抽象，可用数据库行、etcd、Redis 等实现；
`MemoryStore` 是进程内参考实现。`Generator` 在号段耗尽时执行
`CAS(expected=max, next=max+step)`，冲突时重读高水位重试。

## 故障语义

`FaultStore` 在刷盘前后注入故障，用于枚举崩溃场景：

- **刷盘前崩溃**：写入未生效，调用方看到错误，不发号；恢复后序号连续。
- **刷盘后崩溃**：写入已生效但调用方看到错误，不发号；重启后整段跳过。
- **号段耗尽**：触发新一次预留；`uint64` 空间耗尽时返回 `ErrExhausted`。

## 测试

```sh
GO111MODULE=on go test -race ./...
```
