# mvcc — 单写多读的版本化记录存储

纯标准库实现的 MVCC 风格键值存储。所有文件位于调用方 `Open(dir, ...)` 指定的目录
（当前只有一个日志文件 `data.log` 及压缩时的临时文件 `data.log.tmp`）。

## 核心语义

- **单写多读**：同一时刻只允许一个写事务（`Begin` 冲突返回 `ErrWriterActive`），
  读事务（`Snapshot`）任意并发。
- **版本化**：每次 `Commit` 分配全局单调递增的版本号；每个 key 在内存中维护一条
  按版本升序的版本链。
- **读事务固定版本**：`Snapshot` 固定（pin）开启时刻的最新版本，`Get` 返回该版本下
  可见的值——写入后旧读者仍读旧值。
- **删除产生墓碑**：`Delete` 提交一条 tombstone 记录；旧版本读者仍能看到删除前的值，
  新读者读到"不存在"。
- **回收以最老活跃读版本为界**：`GC` 计算 `min(活跃读者固定版本)`（无读者时为最新版本）
  作为上界，丢弃更早的链前缀；长读不阻塞新写，但限制回收。旧读者 `Close` 后，
  链前缀才可释放。整条链不可见且末尾是墓碑时，连墓碑整体移除。
- **明确错误**：写事务重入（已提交/回滚后再 `Put`/`Commit`/`Rollback`）与读事务
  重复关闭、关闭后读取，均返回 `ErrTxClosed`；重复关闭 Store 返回 `ErrStoreClosed`。

## 日志格式与故障注入

每次 `Commit` 把整批操作编码为**一个帧**（事务的原子单位）追加到 `data.log`：

```
| len(4B) | crc32(4B) | version(8B) | opCount(4B) | [op kLen vLen key val]... |
```

`Failpoints` 提供三个注入点（仅测试使用）：`BeforeAppend`、
`AfterAppendBeforeSync`、`AfterSyncBeforeApply`。钩子返回错误即模拟进程在该时刻
崩溃：已落盘的帧保留，重新 `Open` 时由恢复流程重放。

## 崩溃恢复与断电保证的边界

- **进程崩溃**：日志在 `Commit` 返回前已 `write`（`SyncWrites: true` 时已 `fsync`），
  重启后 `Open` 顺序重放所有完整帧，已提交事务不丢失。
- **断电（`SyncWrites: true`）**：`Commit` 在 `fsync` 完成后才返回，已确认的事务
  可抗整机断电（前提：存储设备不虚报 fsync）。断电造成的尾部撕裂写（半个帧、
  CRC 不匹配）在恢复时被检测并**截断到最后一个完整帧**——单帧即事务，故不存在
  半个事务被应用的情况。
- **断电（`SyncWrites: false`）**：`Commit` 只保证数据进入 OS 页缓存，进程崩溃可恢复，
  但整机断电可能丢失最近已确认的事务。这是明确的保证边界。
- **Compact 的原子性**：先写 `data.log.tmp` 并 fsync，再 `rename` 原子替换，最后
  fsync 目录项；压缩中途断电只会留下旧日志或完整新日志之一。压缩时写入一个
  空操作锚点帧保存版本水位线，保证全部数据被回收后版本号也不回退复用。
- **不在保证范围内**：多文件原子更新（当前只有单日志文件，无需）、存储设备
  虚报 fsync、文件系统级损坏。

## 运行样例

```
$ go run ./cmd/demo
目录: /var/folders/.../T/mvcc-demo
旧读者(固定v1) 读 name = alice
新读者(固定v3) 读 name = <不存在>
旧读者存活时 GC 释放条目数 = 0（受最老活跃读版本限制）
旧读者关闭后 GC 释放条目数 = 3
Compact 日志大小: 107 -> 20 字节
重启恢复后: 版本 = 3, name = <不存在>
```

## 测试结果

```
$ go test ./... -v
=== RUN   TestPutGetAndVersioning
--- PASS: TestPutGetAndVersioning (0.00s)
=== RUN   TestOldReaderSeesOldValueAfterWrite
--- PASS: TestOldReaderSeesOldValueAfterWrite (0.00s)
=== RUN   TestDeleteCreatesTombstone
--- PASS: TestDeleteCreatesTombstone (0.01s)
=== RUN   TestGCBoundedByOldestReader
--- PASS: TestGCBoundedByOldestReader (0.01s)
=== RUN   TestGCRemovesDeadTombstoneChain
--- PASS: TestGCRemovesDeadTombstoneChain (0.00s)
=== RUN   TestWriterReentry
--- PASS: TestWriterReentry (0.00s)
=== RUN   TestReadTxDoubleClose
--- PASS: TestReadTxDoubleClose (0.00s)
=== RUN   TestCrashRecoveryAfterSync
--- PASS: TestCrashRecoveryAfterSync (0.02s)
=== RUN   TestCrashBeforeAppendLosesNothing
--- PASS: TestCrashBeforeAppendLosesNothing (0.00s)
=== RUN   TestTornTailTruncatedOnRecovery
--- PASS: TestTornTailTruncatedOnRecovery (0.01s)
=== RUN   TestCompactReclaimsDisk
--- PASS: TestCompactReclaimsDisk (0.35s)
=== RUN   TestStoreDoubleClose
--- PASS: TestStoreDoubleClose (0.00s)
=== RUN   TestCompactPreservesVersionWatermark
--- PASS: TestCompactPreservesVersionWatermark (0.00s)
PASS
ok  	mvcc
```

## 文件结构

- `store.go` — Store、Open/恢复、版本链索引
- `tx.go` — 写事务（单飞）与固定版本读事务
- `log.go` — 帧编码、CRC 校验、重放与撕裂截断
- `gc.go` — GC（链前缀回收）、Compact（日志压缩）、水位线锚点
- `failpoint.go` — 故障注入点
- `errors.go` — 哨兵错误
- `store_test.go` — 全部测试（`go test ./...`）
- `cmd/demo/main.go` — 运行样例
