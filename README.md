# cursorpage

Go 采集 SDK 的断点续传读取器。远端列表在翻页期间持续插入/更新/删除，
进程可能随时崩溃重启；读取器保证：

- 每条最终仍存活的记录至少交付一次（at-least-once）；
- 同一个 `(Key, Version)` 在整个生命周期内最多交付一次；
- 服务端游标过期后自动退回按 `(UpdatedAt, Version)` 的回溯窗口重扫，不漏数。

## 文件结构

- `types.go`：`Record` / `Page` / `RemoteLister` / `Checkpoint` / `Config` 等核心类型。
- `store.go`：`FileStore`，临时文件 + fsync + 原子 rename 的断点落盘。
- `reader.go`：读取器状态机（游标模式、回溯模式、去重、崩溃安全点）。
- `server.go`：模拟服务端，keyset 游标分页、无状态回溯扫描、随机变更、游标过期、墓碑。
- `doc.go`：包文档。
- `reader_test.go` / `memstore_test.go`：崩溃注入与两条交付不变量的核对测试。

## 断点里原子落盘的三类坐标

`Checkpoint` 作为一个整体 JSON 原子写入，同时包含：

1. 服务端游标 `Cursor`、本页服务端视角大小 `CursorPageCount`；
2. 已发出的最后一个键 `LastKey` 及其版本 `LastVersion`、高水位 `HighWaterMark`；
3. 本页已交付位置 `PageDelivered`，以及回溯模式的 `(BTUpdatedAt, BTVersion)` 位置。

去重表 `Delivered`（key -> 最近已交付版本）随断点一起落盘，恢复后继续生效，
超过 `DedupCap` 时淘汰最久未更新的键。

## 游标过期与回溯窗口

- `ListByCursor` 返回包装了 `ErrCursorExpired` 的错误，或游标自然耗尽时，
  读取器切换到 `ListByUpdatedAt` 回溯扫描；
- 回溯锚点 = `高水位 - BacktrackOverlap`，重叠窗口必须覆盖采集滞后预算；
- 回溯扫描无状态、位置全部在断点里，重放请求靠去重表幂等；
- 扫描排空时若高水位在本趟期间被推进过，再开一趟锚点不回退的重叠扫描，
  否则确认完成，避免追赶并发写入时的 O(n²) 全量重扫。

## 崩溃语义

崩溃只通过两个安全点注入（见 `Hooks`）：

- `BeforeFetch`：请求远端之前，崩溃后重放上一页未确认部分；
- `AfterCheckpoint`：断点原子落盘之后，已落盘版本绝不允许重放。

若业务 `Deliver` 回调在自身提交成功后、断点落盘前失败，属于普通
at-least-once 边界，需要下游按 `(Key, Version)` 幂等兜底。

## 运行测试

```sh
go test -race -count=5 ./...
```

主场景 `TestResumableReader_RandomMutationsAndCrashes` 会在翻页间隙随机
插入/更新/删除数据并多次注入崩溃，最后核对无重复版本且存活记录全覆盖；
另有游标过期、页内恢复、回溯新版本、墓碑交付、文件断点端到端恢复等用例。
