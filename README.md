# reactive — 细粒度响应式 Signal 与防钻石依赖更新器

纯 Go 标准库实现（无第三方依赖，兼容 Go 1.11+）。

## 设计

- **Signal**：可写源节点，`Set` 触发更新。
- **Computed**：派生只读节点，创建时通过依赖追踪自动收集依赖。
- **Effect**：副作用节点，依赖变化时执行。

### 防钻石依赖（glitch-free）机制

1. **拓扑染色**：每个节点持有 `rank`（拓扑高度）。Signal 为 0，
   Computed/Effect 的 `rank = max(依赖 rank) + 1`，创建时确定。
2. **两阶段更新**：
   - 阶段一（标脏）：`Set` 后沿 `dependents` 递归标脏，脏节点入最小堆，
     已脏节点跳过——钻石中 D 只入堆一次。
   - 阶段二（刷新）：按 `rank` 拓扑序弹出重算。父节点 rank 恒小于子节点，
     故 D 重算时 B、C 必已刷新完毕，D 读到的是同源同版本的最新值，
     且每轮只执行一次，无中间瞬态脏值。

## 运行

```sh
export GO111MODULE=on
go test ./... -v   # 测试
go run ./cmd/demo  # 钻石图演示
```

## 测试结果

```
=== RUN   TestDiamondUpdatesOnceAndConsistent
--- PASS: TestDiamondUpdatesOnceAndConsistent (0.00s)
=== RUN   TestDiamondEffectSeesNoTransient
--- PASS: TestDiamondEffectSeesNoTransient (0.00s)
=== RUN   TestDeepDiamond
--- PASS: TestDeepDiamond (0.00s)
=== RUN   TestFineGrainedNoSpuriousRecompute
--- PASS: TestFineGrainedNoSpuriousRecompute (0.00s)
PASS
ok  	reactive
```

## 文件

- `reactive.go` — 响应式核心（Signal/Computed/Effect + 调度器）
- `reactive_test.go` — 钻石图、深层钻石、细粒度等测试
- `cmd/demo/main.go` — 运行样例
