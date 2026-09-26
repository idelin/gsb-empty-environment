# traceasm

按 trace/span ID 把乱序到达的遥测 span 事件组装成父子树。仅用 Go 标准库；
事件在本地合成、可由 JSON 夹具回放；不接监控平台，不做展示看板。

## 行为规则

- **子先于父**：父未知时登记为孤儿；父（含根）到达后整棵子树归位。
- **重复 span**：同父为幂等更新（刷新名称等字段）；不同父报 `parent_conflict`，保留首次归属。
- **缺父超时**：超过 TTL 未等到父的孤儿进入 unresolved 并报 `missing_parent_timeout`，绝不挂到任意根下。
- **结构循环**：挂接会成环（如 A↔B 互相为父）时报 `structural_cycle`，拒绝挂接。
- **跨 trace 错误引用**：父 ID 属于其他 trace 时报 `cross_trace_reference`，与结构循环区分开。

时钟由事件时间驱动，`AdvanceTo` 用于回放结束后推进时钟结算超时。

## 运行

```sh
go test ./...                              # 单元测试
go run ./cmd/replay fixtures/late_root.json   # 夹具回放
```

夹具：`fixtures/late_root.json`（子先于父、根晚到）、
`fixtures/conflict_cycle_xtrace.json`（重复/冲突/循环/跨 trace）、
`fixtures/missing_parent.json`（缺父超时）。

## 回放样例

```
$ go run ./cmd/replay fixtures/late_root.json
trace t1
  root "http.request"
    b1 "handler"
      c1 "db.query"
      c2 "cache.get"
issues:
  (none)

$ go run ./cmd/replay fixtures/conflict_cycle_xtrace.json
trace t1
  root "root"
    a "worker-v2"
trace t2
  (unresolved) x "x" structural cycle at parent y
trace t3
  (unresolved) z "foreign-ref" missing parent root
issues:
  [parent_conflict] trace=t1 span=a duplicate span declares parent "other", keeping original parent "root"
  [structural_cycle] trace=t2 span=x attaching "x" under "y" would create a cycle
  [cross_trace_reference] trace=t3 span=z parent "root" belongs to trace "t1"
  [missing_parent_timeout] trace=t3 span=z parent "root" never arrived within 5s

$ go run ./cmd/replay fixtures/missing_parent.json
trace t1
  root "root"
    ok "fine"
  (unresolved) lost "parentless" missing parent ghost
issues:
  [missing_parent_timeout] trace=t1 span=lost parent "ghost" never arrived within 5s
```

## 测试结果

```
$ go test ./... -v
=== RUN   TestChildBeforeParent
--- PASS: TestChildBeforeParent (0.00s)
=== RUN   TestLateRootReattach
--- PASS: TestLateRootReattach (0.00s)
=== RUN   TestDuplicateSameParentIdempotent
--- PASS: TestDuplicateSameParentIdempotent (0.00s)
=== RUN   TestDuplicateDifferentParentConflict
--- PASS: TestDuplicateDifferentParentConflict (0.00s)
=== RUN   TestStructuralCycle
--- PASS: TestStructuralCycle (0.00s)
=== RUN   TestCrossTraceReference
--- PASS: TestCrossTraceReference (0.00s)
=== RUN   TestMissingParentTimeout
--- PASS: TestMissingParentTimeout (0.00s)
=== RUN   TestParentArrivesWithinTTL
--- PASS: TestParentArrivesWithinTTL (0.00s)
PASS
ok  	traceasm
```
