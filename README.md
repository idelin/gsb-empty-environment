# applog：多消费者确认与保留水位的追加日志

纯 Go 标准库实现。所有数据文件都放在调用方指定的目录中：

```
<dir>/<base:020d>.log   段文件，记录格式：[crc32(4B)][length(4B)][payload]
<dir>/MANIFEST          JSON，保存所有消费者的确认游标，通过 tmp+rename 原子替换
```

## 语义

- 追加：`Append(payload) (offset, error)`，offset 从 0 单调递增；满段自动滚动。
- 确认位置 `p` 表示该消费者已消费所有 `offset < p` 的记录。
  - **单调**：`p < 当前位置` 返回 `ErrNonMonotonicAck`。
  - **不越界**：`p > NextOffset()`（已追加/可读范围上界）返回 `ErrAckBeyondHighWater`。
  - **幂等**：`p == 当前位置` 直接返回 nil。确认已落盘但响应丢失时，调用方用相同
    请求重试不会回退位置（`TestAckRetryAfterLostResponse`）。
- 保留水位：所有消费者位置的最小值；水位以下的密封段可回收。
  - 慢消费者未读的段不会被回收（`TestSlowConsumerBlocksSegmentReclaim`）。
  - 消费者撤销（`RemoveConsumer`）后其钉住作用消失，水位随之推进
    （`TestRemovingConsumerAdvancesWatermark`）。
  - 无消费者时没有任何钉住，水位等于高水位；但**活动段（最后一个）永不回收**，
    保证追加始终有目标、高水位有锚点。
  - 已回收 offset 的 `Read` 返回 `ErrOffsetReclaimed`。

## 一致性契约

**游标批量更新（`AckBatch`）**：先对批内全部条目按批前状态校验
（消费者存在、单调、不越界），再以**一次** MANIFEST 提交落盘；任一不合法或提交
失败则整批不生效（all-or-nothing），内存状态也回滚（`TestAckBatchAtomic`、
`TestBatchCommitFailureRollsBackAll`）。

**段回收（`Reclaim`）与游标的关系**：

- 水位只根据**已持久化**的游标计算——任何游标变更都是 MANIFEST fsync+rename+目录
  fsync 成功后才返回，因此 `Reclaim` 绝不会删除尚未被某消费者持久确认的数据。
- 段文件本身是恢复时的事实来源（Open 时扫描目录重建段表），MANIFEST 不登记段。
  所以回收过程中任意时刻崩溃，磁盘上要么是旧文件集合、要么是新文件集合，二者都与
  已提交的 MANIFEST 一致（`TestReclaimCrashRecoveryIsConsistent`）。
- 删除成功后对目录做一次 fsync，保证删除本身持久。

## 故障注入点

通过 `Options.Hooks`（见 `fault.go`）在不改变生产逻辑的前提下模拟故障：

| 注入点 | 时机 | 用法 |
| --- | --- | --- |
| `BeforeSegmentSync` | 记录已写段文件、fsync 前 | 返回错误：Append 失败，未刷盘字节被截断回滚 |
| `AfterSegmentSync` | 段 fsync 完成、Append 返回前 | 崩溃模拟点（丢弃 Log 实例后重新 Open） |
| `BeforeManifestSync` | 新游标写入 tmp、fsync 前 | 返回错误：游标操作失败，落盘状态不变 |
| `BeforeManifestRename` | tmp 已 fsync、rename 前 | 返回错误：旧 MANIFEST 仍然生效 |
| `AfterManifestRename` | rename 完成、目录 fsync 前 | 崩溃模拟点 / “响应丢失”位置 |

模拟进程崩溃的方式：在注入点丢弃 `Log` 实例（不调用 `Close`），然后对同一目录重新
`Open`。撕裂写由测试直接向段文件尾部写坏字节模拟。

## 进程崩溃恢复

`Open` 执行：

1. 读取 MANIFEST 重建消费者游标。
2. 扫描目录得到所有段（按 base offset 排序）。
3. 逐段顺序扫描记录，校验 CRC：遇到部分头、部分体或 CRC 不匹配即为撕裂尾部，
   `truncate` 到最后一条完整记录，并**删除其后的所有段**（尾部之后的数据不可达）。
4. 遇到段文件 base 与前一段计数不连续（连续性缺口，如残留孤儿文件），删除该段及
   之后所有段。
5. 由存活段重建 `NextOffset` 与段内索引。

不变量保证：能够被确认（ack）到的记录在 ack 成功时必然早已随 Append 的 fsync
落盘，因此恢复截断只可能丢失从未被确认的尾部，游标不会指向已消失的记录。

## 断电保证的边界

保证之内：

- `Append` 返回 nil ⇒ 记录已 `write + fsync`；断电后存在。
- `Ack`/`AckBatch`/`RegisterConsumer`/`RemoveConsumer` 返回 nil ⇒ MANIFEST 已
   tmp fsync + 原子 rename + 目录 fsync；断电后游标生效。
- rename 前发生故障（注入或断电）⇒ 看到旧 MANIFEST；rename 后 ⇒ 看到新 MANIFEST，
   不会出现半截 MANIFEST。
- fsync 失败的 Append 会把文件截断回写入前长度，失败调用不占用 offset，重试安全。

保证之外（明确边界）：

- fsync 的持久性最终依赖操作系统/磁盘真正处理 FLUSH。带易失写缓存且不遵守缓存刷新
  的硬件/虚拟化层，任何 fsync 语义都无法由软件单独保证。
- 断电发生在 `BeforeSegmentSync` 与 fsync 完成之间：该次 Append 视为失败，恢复时
  撕裂尾部被丢弃（不会读出脏数据，但该记录不保证存在——它本来也没有成功返回）。
- 本实现 Append 同步落盘（fsync/条），没有组提交/异步刷盘窗口，因此不存在
  “已返回但未落盘”的灰区；代价是写吞吐受限。
- 单个已落盘记录之后的 CRC 损坏（介质位腐烂）不是撕裂尾部，`Read` 返回 CRC 错误，
  恢复扫描会把该位置当作尾部边界处理（其后的数据不再可达）。

## 运行

```sh
go test ./...            # 单元/恢复/并发测试
go test -race ./...      # 含竞态检测
go run ./cmd/demo [目录]  # 生命周期演示（默认用系统临时目录）
```

## 运行样例（go run ./cmd/demo）

```
1) opened log in /private/tmp/applog-demo-run (4 records/segment)
2) append msg-00   -> offset 0
2) append msg-01   -> offset 1
2) append msg-02   -> offset 2
2) append msg-03   -> offset 3
2) append msg-04   -> offset 4
2) append msg-05   -> offset 5
2) append msg-06   -> offset 6
2) append msg-07   -> offset 7
2) append msg-08   -> offset 8
2) append msg-09   -> offset 9
2) append msg-10   -> offset 10
2) append msg-11   -> offset 11
3) high watermark NextOffset=12 (3 segments: 0..3 4..7 8..11)
4) acks: fast=12 slow=3 -> watermark=3 (slowest consumer)
5) reclaim removed 0 segment(s): slow consumer still pins segment 0 (ends at offset 4)
6) removed slow consumer -> watermark=12
7) reclaim removed 2 segment(s); FirstOffset=8, active segment kept
8) Read(0)  -> ErrOffsetReclaimed (segment deleted)
9) Read(11) -> "msg-11" (live record still readable)
10) reopen: NextOffset=12 FirstOffset=8 fast=12 (cursors durable)
11) append after reopen -> offset 12
```

## 测试结果（go test -v -count=1，darwin/arm64, go1.24.2）

```
--- PASS: TestConcurrentAppendAckReclaim (5.84s)   # 并发追加/确认/回收，-race 干净
--- PASS: TestAckMonotonicAndIdempotent            # 单调 + 幂等重试
--- PASS: TestAckBeyondAppendedRange               # 确认不越界
--- PASS: TestAckRetryAfterLostResponse            # 落盘后响应丢失，重试不回退
--- PASS: TestAckBeforeRenameFailureIsNotDurable   # rename 前故障：内存/落盘均回滚
--- PASS: TestAckBatchAtomic                       # 批量游标 all-or-nothing
--- PASS: TestBatchCommitFailureRollsBackAll       # 批量提交故障整批回滚
--- PASS: TestRemoveUnknownAndDuplicate
--- PASS: TestConsumerRegistrationSurvivesCrash
--- PASS: TestAppendReadRoundTrip
--- PASS: TestReadErrors
--- PASS: TestReopenAcrossRotation
--- PASS: TestTornTailRecovery                     # 撕裂尾部截断
--- PASS: TestTornSegmentDropsLaterSegments        # 撕裂段之后的段被丢弃
--- PASS: TestAppendSyncFailureRollsBack           # fsync 故障回滚，不占 offset
--- PASS: TestWatermarkIsSlowestConsumer
--- PASS: TestSlowConsumerBlocksSegmentReclaim     # 慢消费者阻止未读段回收
--- PASS: TestRemovingConsumerAdvancesWatermark    # 撤销后水位推进
--- PASS: TestNoConsumersNeverRemovesActiveSegment # 活动段永不回收
--- PASS: TestReclaimCrashRecoveryIsConsistent     # 回收任意点崩溃后一致
--- PASS: TestReopenedLogHasNoStaleSegments        # 孤儿/缺口段清除
PASS
ok  	applog
```
