# logasm — 多源日志记录重组器

按**起始行模式 + 缩进续行规则**把多源日志流拼接成记录。纯 Go 标准库实现，
测试全部走 `go test`，遥测事件在本地合成、可由 JSON 夹具回放，
不接监控平台，不含展示看板。

## 设计

- `Rules`（rules.go）：起始行正则、缩进续行开关、单条记录字节上限；运行期可热换。
- `assembler`（assembler.go）：单 `(source, epoch)` 流式拼接器。任意切块的文本流入，
  半行缓存到下次写入或 Flush；命中起始行即关闭上一条；缩进行拼入当前记录；
  超限记录**截断并置 `Truncated` 标记、累计 `DroppedBytes`**，绝不静默丢行。
- `Mux`（mux.go）：多源多 epoch 独立重组。行式输入携带 `Seq/LineNo`，
  首行锚定序号游标，乱序行进重排缓冲按序号放行（窗口默认 256，超限按永久缺口跳过）；
  序号缺口、重复行、迟到行、迟到更正分别产生 `seq_gap / duplicate_dropped /
  late_revision / record_revised` 事件。迟到续行按原始行号归属**修订原记录**
  （`Revision+1`、ID 不变），绝不拼进另一条已结束记录。
- `Record`（record.go）：稳定 ID（`source@epoch#seq`）+ 修订版本 + 原始行号
  （`StartLine/EndLine/LineNos`）。
- 检查点（checkpoint.go）：JSON 快照保留**半行、待定记录、重排缓冲、序号游标、
  行号归属、已闭合记录**，恢复后续跑与不间断参考输出一致。
- 背压：总缓冲额度（半行 + 待定记录 + 重排缓冲）耗尽时 `Feed*` 返回
  `ErrBackpressure`，输入不被消费；Flush 释放额度后可重试。
- 遥测（telemetry.go）：`Sink` 接口本地分发，`Recorder` 收集；
  `testdata/replay_fixture.json` 驱动夹具回放比对事件序列。

## 运行

```sh
go test ./...        # 全部测试
go run ./cmd/logasm-demo   # 运行样例
```

## 运行样例（节选）

```
== 1. 流切分：异常堆栈跨 17 字节块仍是一条 ==
  app@1#000002 rev=0 lines=2-4 truncated=false closed=true
      | 2026-10-04 10:00:01 ERROR request failed
      |     java.lang.IllegalStateException: boom
      |         at com.example.Foo.bar(Foo.java:42)
== 3. 序号缺口 + 迟到续行（修订原记录，不拼进他人） ==
  job@1#000001 rev=1 lines=1-2 closed=true
== 7. 背压：总缓冲额度耗尽 ==
second feed err: logasm: buffer budget exhausted, apply backpressure
```

## 测试结果

```
--- PASS: TestExceptionStackAcrossChunks
--- PASS: TestNewStartLineClosesPrevious
--- PASS: TestTruncationMarkedNotSilent
--- PASS: TestIncompleteTailOnFlush
--- PASS: TestOriginalLineNumbersPreserved
--- PASS: TestCheckpointRestorePreservesPartialAndPending
--- PASS: TestCheckpointRestoreSeqState
--- PASS: TestMultiSourceEpochIsolation
--- PASS: TestSeqGapAndLateCorrection
--- PASS: TestLateContinuationNotSplicedIntoOtherRecord
--- PASS: TestDuplicateDropped
--- PASS: TestRuleHotSwapPreservesIdentity
--- PASS: TestChunkedAndOutOfOrderMatchReference
--- PASS: TestBackpressureOnBudgetExhaustion
--- PASS: TestTelemetryFixtureReplay
PASS
ok  	logasm
```
