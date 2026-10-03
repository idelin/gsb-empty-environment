# ARIES 崩溃恢复引擎（Go，仅标准库）

实现数据库经典 ARIES 崩溃恢复协议的三阶段扫描，以及配套的紧凑二进制
WAL 编解码与基于 mmap 的零拷贝解析。

## 结构

- `wal/` — 预写日志
  - `record.go` 记录类型（UPDATE/COMMIT/ABORT/CLR/CHECKPOINT/END）
  - `codec.go` 40 字节定长头 + CRC32 的紧凑二进制编解码；所有读取先边界
    检查，任何截断/腐化输入只返回错误、绝不 panic；版本字段支持向后兼容
    下限校验与向前兼容（跳过未知尾部扩展字节）
  - `writer.go` 顺序追加写，LSN 即文件字节偏移，并发安全
  - `reader.go` `syscall.Mmap` 只读映射，解码出的 Before/After 镜像直接
    引用映射区（零拷贝）；`ScanValid` 容忍崩溃留下的撕裂尾部
- `recovery/` — 恢复引擎
  - `engine.go` Analysis（从事务检查点重建 ATT/DPT）→ Redo（从最小
    recLSN 历史重复，按 pageLSN 跳过已持久化记录）→ Undo（按最大 LSN
    反向扫描，逐条写 CLR 补偿，链尽写 END；崩溃中重启恢复幂等）
  - `scenario.go` 验收场景：5 个事务（2 提交 / 2 运行 / 1 中止中）、
    检查点之后崩溃
- `cmd/aries-demo/` — 端到端运行样例

## 运行

```sh
go test -race ./...   # 测试（含竞态检测）
go run ./cmd/aries-demo
```

## 运行样例输出

```
pre-crash: 13 log records, winners=[1 2] losers=[3 4 5], log bytes=800
*** CRASH *** (no END records written for open transactions)

[analysis] transaction table:
  txn=1 lastLSN=362 status=1
  txn=2 lastLSN=464 status=1
  txn=3 lastLSN=738 status=0
  txn=4 lastLSN=504 status=0
  txn=5 lastLSN=668 status=2
[analysis] dirty page table:
  page=1 recLSN=0
  page=2 recLSN=62
  page=3 recLSN=238

[redo] startLSN=0 re-applied=9 skipped=0
[undo] losers=[3 4 5] CLRs written=3 ENDs written=5

[result] durable pages after recovery:
  page=1 pageLSN=990 data="AAAA1111\x00\x00\x00\x00" OK
  page=2 pageLSN=880 data="BBBB2222\x00\x00\x00\x00" OK
  page=3 pageLSN=1100 data="\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00" OK

recovery verified: committed data persisted, uncommitted data rolled back
```

## 测试结果（go test -race ./...）

```
ok  aries/recovery  — TestCrashRecoveryAcceptance / TestRecoveryIdempotent /
                      TestRedoRespectsPageLSN / TestConcurrentStorage /
                      TestNoGoroutineLeak
ok  aries/wal       — TestCodecRoundTrip / TestCodecMaxImage /
                      TestDecodeNeverPanics / TestDecodeCorruptions(9 子用例) /
                      TestForwardCompatibility / TestReaderRoundTripAndZeroCopy /
                      TestScanValidToleratesTornTail / TestConcurrentAccess /
                      TestNoGoroutineLeak
```

测试覆盖：全类型编解码往返、64KiB 最大镜像与超限拒绝、逐字节截断与
逐位翻转不 panic、九类腐化输入、新版本前向兼容、mmap 零拷贝指针断言、
撕裂尾部容忍、并发读写（-race）、goroutine 无泄漏断言，以及验收边界：
检查点后崩溃、三阶段扫描、未提交事务精确回滚、已提交数据持久化。
