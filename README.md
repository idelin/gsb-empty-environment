# 审计事件背压不漏报

安全钩子的审计接收器满载时，不能先执行敏感操作再丢日志。实现供Go钩子宿主调用的提交包，操作先获得持久审计槽位，事件写入确认后才调用副作用；回调失败记录同一操作ID的失败结果，崩溃后未决操作不能自行重做。用有界存储和故障点验证背压、重复请求与先落日志后断电。

## 协议保证

`Committer.Commit` 严格按以下顺序执行，任何一步失败都不会越过它后面的步骤：

1. **获取有界槽位**：槽位数量由 `Config.MaxPending` 限制。槽位全满时 `Commit`
   阻塞等待或随 `ctx` 取消，形成显式背压；此时副作用完全不会被调用。
2. **持久预留**：向 `Sink` 追加一条 `StatusPending` 记录。只有 `Append` 返回
   nil（文件 Sink 内部已完成 write + fsync）后才继续。
3. **执行副作用**：每个 `OpID` 在整个日志生命周期内最多执行一次。
4. **记录终态**：成功追加 `StatusSucceeded`；副作用返回错误时追加
   `StatusFailed`，错误信息写入同一条记录并随 `Commit` 返回，审计事件不会丢失。

### 重复请求

- 并发的相同 `OpID` 合并为一次执行，等待者共享同一结果。
- 已有终态记录的 `OpID` 幂等返回存储结果（`Result.Replayed == true`），不再执行。
- 处于 `Pending` 的 `OpID`（典型于崩溃恢复）返回 `ErrPending`，绝不自动重做；
  宿主确认真实结果后调用 `Resolve` 补记终态。

### 崩溃与恢复

- 预留已确认但终态未写入时进程崩溃：重启后重放日志得到 `Pending` 列表
  （`Committer.Pending`），副作用不会被重新触发，保证至多一次。
- 写入中途断电产生的撕裂尾帧在打开时被安全截断；完整帧的 CRC 校验失败则按
  `ErrCorrupt` 致命处理，不会静默丢弃已确认事件。

### 有界存储

- 槽位上限控制同时未决的操作数（内存背压）。
- `FileSink`/`MemorySink` 的字节上限控制日志体积；超限时先压缩（仅保留每个
  `OpID` 的最新记录，`Pending` 记录永不删除），仍放不下则返回 `ErrSinkFull`。

## 用法

```go
sink, err := netsec.NewFileSink("/var/lib/hook/audit.wal", 1<<20, netsec.Faults{})
if err != nil { /* 处理 */ }

committer, err := netsec.New(ctx, sink, netsec.Config{MaxPending: 64})
if err != nil { /* 处理 */ }

res, err := committer.Commit(ctx,
    netsec.Operation{OpID: id, Actor: user, Action: "grant-role"},
    func(ctx context.Context) error { return grantRole(user) },
)
```

## 故障点与验证

`Faults.BeforeAppend` 模拟"确认前断电"（无落盘、无副作用）；
`Faults.AfterAppend` 模拟"fsync 后、继续前崩溃"（记录可在重开后存活）。
测试覆盖槽位背压、字节背压、串行/并发重复、副作用失败落账、先落日志后断电、
撕裂尾帧与 CRC 损坏，以及真实文件 Sink 的 fsync 落盘与重启恢复。
