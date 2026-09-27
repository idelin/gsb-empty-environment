# Critical Trace DAG

一个只使用 Go 标准库的本地追踪分析示例。它从合成遥测事件回放 span，根据 `child_of` 和 `follows_from` 因果关系构造 DAG，并计算：

- `timeline_wall`：所有有效 span 覆盖的观察墙钟范围。
- `accumulated_work`：所有有效 span 的独占工作量之和；父 span 会减去直接同步子 span 的时间并集。
- `critical_wall`：最长因果路径的墙钟跨度，包含异步等待空隙。
- `critical_work`：关键路径上的独占工作量；共享后继只出现一次。
- `async_wait`：关键路径墙钟中没有执行工作量的等待部分。

负时长、因果违例、缺失引用和环依赖都作为独立问题报告。存在环时不生成关键路径，避免沿环构造伪路径。

## 夹具场景

- `trace-good`：两个并行 10ms 子任务、4ms 异步等待和一个共享后继。
- `trace-invalid`：负时长 span、异步因果违例和缺失引用。
- `trace-cycle`：三个零时长 span 形成 `follows_from` 环。

事件只在 `fixture.go` 本地合成，不连接监控平台或展示服务。

## 运行

```sh
go test -v ./...
go run .
```

如果本机 Go 缓存目录不可写，可显式指定仓库内缓存：

```sh
GOCACHE=$PWD/.gocache go test -v ./...
GOCACHE=$PWD/.gocache go run .
```

## 测试结果

```text
=== RUN   TestParallelChildrenDoNotDoubleCountParentWall
--- PASS: TestParallelChildrenDoNotDoubleCountParentWall
=== RUN   TestAsyncWaitAndSharedSuccessorCountedOnce
--- PASS: TestAsyncWaitAndSharedSuccessorCountedOnce
=== RUN   TestNegativeDurationReportedAndExcluded
--- PASS: TestNegativeDurationReportedAndExcluded
=== RUN   TestCausalViolationDoesNotCreateCriticalPath
--- PASS: TestCausalViolationDoesNotCreateCriticalPath
=== RUN   TestCycleReportedWithoutPseudoPath
--- PASS: TestCycleReportedWithoutPseudoPath
=== RUN   TestReplayToleratesLateReferenceDelivery
--- PASS: TestReplayToleratesLateReferenceDelivery
=== RUN   TestReplayRejectsIncompleteAndUnknownEvents
--- PASS: TestReplayRejectsIncompleteAndUnknownEvents
=== RUN   TestSampleFixture
--- PASS: TestSampleFixture
PASS
ok  	criticaltrace
```

## 样例输出

```text
trace=trace-cycle status=cycle spans=3 valid=3
  timeline_wall=0s accumulated_work=0s
  critical_path=none (dependency cycle; no pseudo-path is emitted)
  problem[CYCLE_DETECTED/error] span=alpha,beta,gamma: dependency cycle: alpha -> beta -> gamma -> alpha

trace=trace-good status=ok spans=4 valid=4
  timeline_wall=22ms accumulated_work=28ms
  critical_wall=22ms critical_work=18ms async_wait=4ms path=[root left join]

trace=trace-invalid status=invalid spans=3 valid=2
  timeline_wall=15ms accumulated_work=20ms
  critical_wall=10ms critical_work=10ms async_wait=0s path=[consumer]
  problem[CAUSAL_VIOLATION/error] span=consumer: follows_from span starts before predecessor producer ends
  problem[NEGATIVE_DURATION/error] span=negative: span ends before it starts
  problem[UNRESOLVED_REFERENCE/error] span=consumer: follows_from reference points to missing span missing
```
