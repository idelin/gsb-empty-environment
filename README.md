# reactive — 细粒度响应式 Signal 与防钻石依赖更新器

纯 Go 标准库实现（无第三方依赖），提供 `Signal` / `Computed` / `Effect` 响应式核心。

## 防钻石依赖（glitch-free）原理

钻石图 `A -> B, A -> C, (B,C) -> D` 中，朴素实现会让 D 因 B、C 分别通知而执行两次，
且第一次执行时可能读到「新 B + 旧 C」的中间瞬态脏值。本实现通过两阶段更新避免：

1. **阶段一 · 拓扑染色**：`Signal.Set` 后向下游传播标记——直接订阅者染 `dirty`，
   更远的下游染 `check`（可能脏）。
2. **阶段二 · 按拓扑序结算**：调度器按节点层级（`level = max(依赖层级)+1`）从小到大
   处理队列，computed 一律先于 effect。处理 `check` 节点时先递归结算其依赖，
   再比对依赖版本号，仅当依赖真的变化才重算。

因此 D 被处理时 B、C 已结算完毕：**D 每次源变更只计算一次，且读到的 B、C
是属于同一版本 A 的一致快照**。此外：

- 值未实际变化（`reflect.DeepEqual`）时不提升版本号，`check` 节点可跳过重算；
- 无订阅者的 computed 保持脏标记，`Get` 时才惰性结算（按需更新）；
- 相同值的 `Set` 是 no-op。

## 文件

- `reactive.go` — 响应式核心（Signal / Computed / Effect / 调度器）
- `reactive_test.go` — 验收测试（`go test`）
- `cmd/demo/main.go` — 钻石图运行样例

## 运行样例

```
$ go run ./cmd/demo
  [compute] B = A+1 = 2
  [compute] C = A*2 = 2
  [compute] D = B+C = 2+2 = 4  (consistent=true)
  [effect]  D = 4
--- initial ---
counts: B=1 C=1 D=1
--- A.Set(2) ---
  [compute] B = A+1 = 3
  [compute] C = A*2 = 4
  [compute] D = B+C = 3+4 = 7  (consistent=true)
  [effect]  D = 7
counts: B=2 C=2 D=2  (each must be 2: initial + exactly one update)
--- A.Set(3) ---
  [compute] B = A+1 = 4
  [compute] C = A*2 = 6
  [compute] D = B+C = 4+6 = 10  (consistent=true)
  [effect]  D = 10
counts: B=3 C=3 D=3
final: A=3 B=4 C=6 D=10
```

每次 `A.Set` 后 B、C、D 的计数恰好 +1，且 D 每次计算时 `consistent=true`
（读到的 B、C 来自同一版本的 A，无中间瞬态）。

## 测试结果

```
$ go test -v ./...
=== RUN   TestDiamondGlitchFree
--- PASS: TestDiamondGlitchFree (0.00s)
=== RUN   TestCheckNodeSkippedWhenDepValueUnchanged
--- PASS: TestCheckNodeSkippedWhenDepValueUnchanged (0.00s)
=== RUN   TestUnobservedComputedIsLazy
--- PASS: TestUnobservedComputedIsLazy (0.00s)
=== RUN   TestSetSameValueIsNoop
--- PASS: TestSetSameValueIsNoop (0.00s)
=== RUN   TestEffectDispose
--- PASS: TestEffectDispose (0.00s)
=== RUN   TestRepeatedUpdates
--- PASS: TestRepeatedUpdates (0.00s)
PASS
ok  	reactive
```

- `TestDiamondGlitchFree` — 验收边界：钻石图 A->B, A->C, (B,C)->D；更新 A 后
  B、C 各更新一次，D 仅计算 1 次，且 D 读到的 B、C 始终满足一致快照约束
  `C == 2*(B-1)`，effect 每次变更也只跑一次。
- `TestCheckNodeSkippedWhenDepValueUnchanged` — `check` 染色生效：依赖重算但
  值未变时下游不重算。
- `TestUnobservedComputedIsLazy` — 无订阅者的 computed 按需（惰性）更新。
- `TestSetSameValueIsNoop` / `TestEffectDispose` / `TestRepeatedUpdates` —
  相同值 no-op、退订、连续 100 次更新每次 D 只算一次。

> 注：沙箱/受限环境下若构建缓存目录不可写，可设 `GOCACHE=/tmp/gocache` 后再运行。
