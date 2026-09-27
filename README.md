# traceasm — 按 trace/span ID 组装父子关系

纯 Go 标准库实现。遥测事件由本地夹具（`fixtures/*.json`）合成并回放，
不接监控平台，不做展示看板；核心是一个可测试的组装器（`Assembler`）。

## 设计

- `Assembler.Ingest(span, at)` 摄入 span，返回本次引发的结构化事件；
  `Assembler.Expire(at)` 推进超时；`Assembler.Tree(traceID)` 输出森林视图。
- **子先于父**：父缺失时进入 `waiting` 队列并发出 `OrphanPending`，
  父到达时递归归位（`OrphanResolved`），支持多级链式乱序。
- **根晚到**：根（无 parent）注册为 `RootRegistered` 后，等待它的孤儿整链归位。
- **重复更新**：同 trace 同 spanID 且父相同 → `DuplicateUpdated`，合并可变字段；
  父不同 → `ConflictDetected`，保留先到的父，拒绝后者。
- **缺父超时**：超过 `orphanTimeout` 未等到父 → `OrphanExpired`，
  保持 unattached，**绝不挂到任意根下**。
- **结构循环 vs 跨 trace 引用**：
  - 挂载前沿父链检查回环（含自引用 `parent == self`）→ `CycleDetected`，
    成环 span 保持 unattached；
  - parentSpanID 存在于**其他** trace → `CrossTraceReference`，
    按孤儿处理直至超时，不与结构循环混淆。

## 运行样例

```console
$ go run ./cmd/replay fixtures/out_of_order.json
== events ==
[1s] OrphanPending        trace=t1 span=s3 parent=s2 -- waiting for parent, deadline in 5s
[2s] OrphanPending        trace=t1 span=s2 parent=s1 -- waiting for parent, deadline in 5s
[2s] OrphanResolved       trace=t1 span=s3 parent=s2
[3s] RootRegistered       trace=t1 span=s1
[3s] OrphanResolved       trace=t1 span=s2 parent=s1
[4s] DuplicateUpdated     trace=t1 span=s2 parent=s1 -- same parent; mutable fields merged
== forests ==
trace t1:
  s1 (root) [root]
    s2 (child-renamed) [attached]
      s3 (grandchild) [attached]
```

```console
$ go run ./cmd/replay fixtures/cross_trace.json
== events ==
[0s] RootRegistered       trace=tA span=s1
[1s] CrossTraceReference  trace=tB span=s9 parent=s1 -- parent "s1" lives in a different trace; kept pending, never re-rooted
[1s] OrphanPending        trace=tB span=s9 parent=s1 -- waiting for parent, deadline in 5s
[11s] OrphanExpired        trace=tB span=s9 parent=s1 -- parent never arrived; left unattached
== forests ==
trace tA:
  s1 (root-A) [root]
trace tB:
  (unattached, waiting-for=s1)
    s9 (stray) [expired]
```

其余夹具：`conflict.json`（重复 span 不同父）、`cycle.json`（互相为父 + 自引用）、
`orphan_timeout.json`（缺父超时且不挂到根）。

## 测试结果

```console
$ go test ./... -v
=== RUN   TestChildBeforeParentThenLateRoot
--- PASS: TestChildBeforeParentThenLateRoot (0.00s)
=== RUN   TestDuplicateSameParentMerges
--- PASS: TestDuplicateSameParentMerges (0.00s)
=== RUN   TestDuplicateDifferentParentConflicts
--- PASS: TestDuplicateDifferentParentConflicts (0.00s)
=== RUN   TestMissingParentExpiresAndIsNotReRooted
--- PASS: TestMissingParentExpiresAndIsNotReRooted (0.00s)
=== RUN   TestOrphanSurvivesUntilDeadline
--- PASS: TestOrphanSurvivesUntilDeadline (0.00s)
=== RUN   TestStructuralCycleDetected
--- PASS: TestStructuralCycleDetected (0.00s)
=== RUN   TestCrossTraceReferenceIsNotAStructuralCycle
--- PASS: TestCrossTraceReferenceIsNotAStructuralCycle (0.00s)
=== RUN   TestRecursiveOrphanChain
--- PASS: TestRecursiveOrphanChain (0.00s)
PASS
ok  	traceasm
```

## 文件

- `span.go` — Span / Event 类型与事件分类
- `assembler.go` — 组装器核心（乱序、重复、超时、循环、跨 trace）
- `assembler_test.go` — 全部行为的 `go test` 用例
- `cmd/replay/main.go` — 夹具回放器，打印事件流与最终森林
- `fixtures/*.json` — 本地合成的遥测夹具
