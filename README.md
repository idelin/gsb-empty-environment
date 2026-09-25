# diskqueue — 磁盘优先的持久化优先级队列（Go 标准库实现）

## 特性

- **稳定序号**：每个任务入队时分配单调递增的 `seq`，重启后不重置、不复用。
- **稳定排序**：优先级小者先出队（**允许负优先级**）；相同优先级按入队顺序（FIFO）。
- **优先级更新 / 删除**：`Update(seq, prio)`、`Delete(seq)`；对已删任务更新返回 `ErrNotFound`。
- **原子批量弹出**：`PopBatch(n)` 先落盘 commit 记录再移除，失败不丢任务。
- **持久化**：所有变更先写 WAL（带 CRC 的帧），重启回放恢复；`Compact()` 做快照压缩。
- **故障注入**：`WithFaultHook` 在写前 / fsync 前 / fsync 后三个注入点模拟故障。
- 所有文件都在 `Open(dir)` 指定目录下：`wal.log`、`snapshot`（及临时 `snapshot.tmp`）。

## 用法

```go
q, _ := diskqueue.Open("/path/to/dir")          // 文件全部在该目录下
seq, _ := q.Enqueue(-10, []byte("线上告警"))     // 负优先级 OK，返回稳定序号
q.Update(seq, 100)                               // 改优先级
q.Delete(seq)                                    // 删除
items, _ := q.PopBatch(32)                       // 原子批量弹出，同优先级按入队序
q.Compact()                                      // 快照压缩 WAL（可选）
q.Close()
```

运行样例：`go run ./cmd/demo /tmp/dq-demo`；测试：`go test ./...`

## 崩溃恢复与断电保证边界

**写入路径**：每条记录（Enqueue/Update/Delete/PopCommit）以 `[len][type][payload][crc32]`
帧追加到 `wal.log`，默认每条 `fsync`（`WithSyncOnWrite(false)` 可关闭）。

**进程崩溃（OS 未崩）**：已 `write()` 的数据在 OS 页缓存中，进程死亡不丢失。
重启时 `Open` 回放 WAL 恢复全部状态，即使未 `fsync`。崩溃可能留下写了一半的
尾帧，回放时 CRC/长度校验失败即截断（`Truncate`），从最后一个完好帧继续。

**断电 / OS 崩溃**：只有 `fsync` 返回的记录保证落盘。
- `syncOnWrite=true`（默认）：每个已返回成功的操作都持久；
- `syncOnWrite=false`：最近一批已确认操作可能丢失，但文件不会损坏（撕裂尾被截断）。
- 文件创建、快照 `rename` 后对目录做 `fsync`，保证目录项持久。

**PopBatch 原子性**：commit 记录（含本批全部 seq）**始终 fsync**，然后才从内存移除。
- commit 落盘前失败 → 返回错误，任务原样保留，**不丢**；
- commit 落盘后、内存应用前崩溃 → 重启回放 commit，任务被移除，**不重复交付**；
- 即“弹出”对调用方是恰好一次：要么拿到任务且任务已持久移除，要么报错且任务仍在。

**旧记录不复活**：回放是幂等的——`Update/Delete/PopCommit` 对不存在（已删/已弹出/
已合并进快照）的 seq 一律为无操作；`Enqueue` 序号小于快照水位则跳过。因此滞留在
WAL 尾部的旧 Update 记录无法让已删除任务复活。

**快照**：`Compact()` 先写 `snapshot.tmp` + `fsync`，再 `rename` 原子替换，最后重建空
WAL。rename 与 WAL 清空之间崩溃也无妨：旧 WAL 记录对新快照重放是幂等的。

**不保证**：多进程同时打开同一目录（单进程内并发安全，由互斥锁保护）；
文件系统本身损坏（非撕裂）不在恢复范围内。

## 文件布局

| 文件 | 说明 |
|---|---|
| `queue.go` | 队列主体：Open/Enqueue/Update/Delete/PopBatch/Compact/Close |
| `wal.go` | WAL 帧编解码、追加、带回放截断的恢复 |
| `snapshot.go` | 快照读写（tmp+rename）、目录 fsync |
| `heap.go` | 二叉堆（优先级，序号 tie-break） |
| `fault.go` | 故障注入点定义 |
| `queue_test.go` | 全部测试（`go test ./...`） |
| `cmd/demo/main.go` | 运行样例 |
