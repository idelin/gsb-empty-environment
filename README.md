# pagestore — WAL 页更新事务存储(教学实现)

纯标准库 Go 实现:页更新事务、先写日志再写数据页、恢复时只重放带完整
提交标记的事务。所有文件落在调用方指定的目录,故障通过注入点模拟。

## 设计

- **提交协议**:事务的 begin / 页镜像 / commit 记录一次性连续写入 WAL
  并 `fsync` —— 这次 fsync 就是**提交点**。之后数据页才允许在
  checkpoint / 恢复时落盘(日志先于数据页)。
- **页格式**:`version(8) | crc32(4) | data(4096)`。校验和覆盖
  version+data;version 即产生该页镜像的 WAL LSN。全零页约定为
  "从未写入"的空页(版本 0)。
- **WAL 记录**:`magic | len | lsn | txn | type | pageID | payload | crc32`。
  物理日志:page 记录携带整页镜像。
- **恢复**:扫描 WAL → 只重放含 commit 记录的事务 → 仅当记录 LSN 大于
  盘上页版本时才应用(**幂等**,重放两次结果相同)→ 校验所有数据页 →
  截断残缺日志尾。LSN 计数器恢复为 max(WAL 记录, 盘上页版本),保证
  跨重启单调。
- **残缺尾 vs 中间损坏**:解析失败时向后搜索下一条完整有效记录
  (magic+长度+CRC 自洽)。找不到 → 崩溃截断的尾巴,截断即可;
  找得到 → 中间损坏,**报错拒绝打开**,绝不静默丢弃后面的有效记录。

## 故障注入点(`db.SetFailpoint`)

| 注入点 | 模拟 |
|---|---|
| `commit.beforeAppend` | 生成 WAL 记录前崩溃 |
| `commit.beforeSync` | 记录已组好、一个字节都没写盘前崩溃 |
| `commit.afterSync` | commit 记录已 fsync(事务已持久)后崩溃 |
| `checkpoint.beforeSync` | 数据页写完但未 fsync 时崩溃 |
| `pages.tornWrite` | 掉电把页只写了一半(撕裂页) |

注入错误返回后应丢弃该 DB 句柄并重新 `Open(dir)`,等价于进程崩溃重启。

## 保证边界

**进程崩溃(完整保证)**
- 提交点 = commit 记录 fsync 完成。之前崩溃:事务整体消失,绝不会暴露
  半个事务(记录整批一次写入,未提交数据只在内存缓冲区)。之后崩溃:
  恢复时整体重放。
- 数据页只在 checkpoint/恢复时写入,因此页文件里永远不会有未提交数据。

**断电(有条件保证)**
- 前提:存储栈不谎报 `fsync`(设备易失写缓存已正确冲刷)。本实现按
  WAL→数据页→目录 的顺序 fsync,并对目录 fsync 保证文件创建的持久性。
- 撕裂数据页:页校验和识别,恢复时用 WAL 中的整页镜像修复。
- 撕裂日志尾(半条记录/垃圾字节):识别并截断,已提交前缀不受影响。

**不保证(检测并报错,而非静默继续)**
- WAL 中间损坏(位腐、misdirected write):`Open` 返回错误。因为后面
  还有有效记录,截断会丢已提交数据,只能人工介入。
- 已提交 WAL 本身被介质损坏覆盖、文件系统/硬件谎报 fsync、整盘失效:
  超出单节点 WAL 的能力边界,需要复制/备份解决。
- 数据页损坏且 WAL 已截断(无覆盖记录):恢复时检测并报错。

## 运行

```sh
go test ./...      # 全部测试
go run ./cmd/demo  # 崩溃注入演示(可选参数:数据目录)
```

## 测试结果(go test ./... -v)

```
--- PASS: TestCommitReadAndReopen
--- PASS: TestUncommittedNotVisible
--- PASS: TestCrashBeforeCommitSyncDiscardsTxn (commit.beforeAppend / commit.beforeSync)
--- PASS: TestCrashAfterCommitSyncReplays
--- PASS: TestReplayTwiceIdempotent
--- PASS: TestTruncatedTailIgnored
--- PASS: TestCorruptTailRecordDiscardsLastTxn
--- PASS: TestMidLogCorruptionFails
--- PASS: TestTornPageRepairedFromWAL
--- PASS: TestPageChecksumDetectsCorruption
--- PASS: TestPageVersionMonotonic
--- PASS: TestLSNSurvivesCheckpointAndRestart
PASS
ok  	pagestore
```

## 运行样例(go run ./cmd/demo)

```
1) txn1 commits pages 0,1 and checkpoints
    page 0: version=2 data="hello"
    page 1: version=3 data="world"
2) txn2 crashes BEFORE the commit fsync -> discarded, no partial writes
   commit err: simulated crash
    page 0: version=2 data="hello"
    page 1: version=3 data="world"
3) txn3 crashes AFTER the commit fsync -> replayed by recovery
   commit err: simulated crash
    page 0: version=5 data="durable"
4) power loss halfway through a page write -> checksum catches it, WAL repairs it
   checkpoint err: pagestore: injected torn page write
    page 2: version=8 data="torn-target"
5) crash while appending to the WAL -> torn tail detected and truncated
   reopened OK; committed data intact:
    page 0: version=5 data="durable"
done.
```
