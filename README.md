# pairlog

仅依赖 Go 标准库的持久化追加日志：段文件存储、多消费者确认游标、保留水位与段回收，支持故障注入与崩溃恢复。所有文件都写在 `Open(dir, ...)` 指定的目录中。

## 文件布局

```
<dir>/
  flock                  进程内排他目录锁（flock，内容为空）
  state.json             游标状态：水位、日志末尾、活跃段基址、各消费者 ack
  state.json.tmp         状态原子提交的临时文件（rename 前）
  segment-00000000000000000000.log   段文件，按基址偏移命名
```

记录帧格式（大端）：`offset(8) | payload长度(4) | CRC32(4) | payload`。

## 使用

```go
l, _ := pairlog.Open("/path/to/logdir", pairlog.Options{MaxSegmentBytes: 64 << 20})
defer l.Close()

l.RegisterConsumer("worker-1")          // 新消费者从当前日志末尾开始
off, _ := l.Append([]byte("hello"))     // 追加并 fsync 后返回
rec, _ := l.Read("worker-1")            // 至少一次投递；io.EOF 表示已追上
l.Ack(map[string]uint64{"worker-1": rec.Next})  // 批量、原子、单调
res, _ := l.Reclaim()                   // 回收所有消费者都已确认的密封段
```

## 一致性契约

**确认（Ack）**

- 批量原子：一批更新先整体校验（消费者存在、目标为记录边界、不回退、未越读），任一失败则整批不生效；校验通过后一次原子状态提交全部落盘。
- 单调：确认位置只能前进。`pos < 当前ack` 返回 `ErrAckRegression` 且位置不变。
- 不越读：`pos > 本进程已投递的 readHigh` 返回 `ErrAckBeyondRead`。readHigh 不持久化，重启后回退到已落盘的 ack，记录会被重新投递（at-least-once）。
- 幂等重试：`pos == 当前ack` 是成功的空操作。确认落盘后响应丢失，客户端重试同一确认不会回退位置；重试更旧的值会被拒绝且位置不变。

**段回收（Reclaim）**

- 目标水位 = 所有已注册消费者 ack 的最小值（无消费者时为日志末尾）；慢消费者会阻止其未读段被回收，消费者撤销（`UnregisterConsumer`，持久生效）后水位随之推进。
- 只有完整位于水位之下（`end <= minAck`）的密封段被删除；活跃段永不删除，水位始终落在段边界上。
- 提交顺序：先 unlink 段文件，再原子提交新水位。恢复时水位取“状态中的水位”与“最老存活段基址”的较大者，因此崩溃只可能少删、不可能误删未确认数据，也不会让水位回退。

**状态提交**：`state.json.tmp` 写入 → fsync → rename → 目录 fsync。任何一步之前崩溃，恢复后要么看到完整旧状态，要么看到完整新状态。

## 故障注入

`Options.FaultHook` 返回非 nil 错误即模拟进程在该点崩溃：内存状态被丢弃，`Log` 关闭（后续调用返回 `ErrClosed`），需重新 `Open` 恢复。注入点：

| 注入点 | 位置 |
|---|---|
| `state:before_write` | 临时状态文件写入前 |
| `state:before_sync` | 临时状态文件 fsync 前 |
| `state:before_rename` | rename 前 |
| `state:before_dir_sync` | rename 后、目录 fsync 前 |
| `segment:before_write` | 记录写入前 |
| `segment:before_sync` | 记录写入后、fsync 前 |
| `reclaim:before_state` | 段文件 unlink 后、水位提交前 |

## 崩溃恢复与断电边界

**进程崩溃**（进程退出、OS 与磁盘仍在）：已 fsync 的数据一定还在。恢复时扫描段文件、截断活跃段的撕裂尾部、用段文件重建 `logEnd`/`activeBase`、按上表归一化水位，并校验每个消费者游标仍在 `[watermark, logEnd]` 内且落在记录边界上。

**断电**（内核/page cache 丢失）：保证边界以 fsync 为准——

- `Append` 返回前已 fsync 段文件：返回成功的记录断电后不丢；未返回的写入可能部分落盘，恢复时按 CRC/长度截断，日志保持连续。
- `Ack`/`Register`/`Unregister` 返回前完成 rename + 目录 fsync：返回成功的游标断电后不丢；崩溃在提交中途时，恢复后看到完整旧状态或完整新状态之一，客户端重试安全（幂等）。
- `Reclaim` 的 unlink 与新水位提交之间断电：可能出现“文件已删、水位未推进”，恢复时水位被提升到最老存活段基址，语义等价于回收已完成；不会出现水位超过存活数据的情况。
- 段创建/轮转的目录项在返回前做目录 fsync。

边界说明：以上保证依赖 OS/文件系统/硬件尊重 fsync 语义；`state.json` 单文件无副本，若文件系统本身返回损坏的状态文件（非撕裂写），`Open` 返回 `ErrCorrupt` 而不是猜测恢复。

## 运行

```sh
go test ./...            # 全部测试（含故障注入与恢复场景）
go run ./cmd/pairlog-demo [dir]   # 运行样例（目录需为空或不存在）
```

测试结果（Go 1.24, darwin/arm64）：11 个测试全部通过，覆盖确认单调性/越读拒绝/批量原子性、慢消费者阻塞回收、撤销后水位推进、崩溃后游标恢复、确认落盘后响应丢失的重试、回收中途崩溃的一致性、撕裂写截断等场景。

```
--- PASS: TestAppendReadAckRules
--- PASS: TestBatchAckIsAtomic
--- PASS: TestSlowConsumerBlocksReclaim
--- PASS: TestUnregisterAdvancesWatermark
--- PASS: TestCrashRecoveryPersistsCursors
--- PASS: TestAckResponseLostRetryKeepsPosition
--- PASS: TestCrashBeforeStateCommitKeepsOldCursor
--- PASS: TestReclaimCrashConsistency
--- PASS: TestTornTailIsTruncatedOnRecovery
--- PASS: TestRegisterStartsAtLogEnd
--- PASS: TestSecondOpenFailsWhileLocked
ok  	pairlog	1.532s
```
