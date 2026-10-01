# routemig — 多版本路由拓扑渐进式迁移控制器

纯 Go 标准库实现。在旧拓扑 `Top_Old` 与新拓扑 `Top_New` 双共存期间，
按百分比灰度推移流量，并通过“键版本目录”保证同一键的读写永远落在同一代
拓扑，从机制上杜绝双写分裂。

## 设计

```
                 ┌──────────────────────────────┐
   read/write ──▶│          Controller          │
                 │  grayPercent (0..100)        │
                 │  key directory: key→Version  │
                 └───────┬──────────────┬───────┘
                  VersionOld            VersionNew
                         │              │
                 ┌───────▼──────┐ ┌─────▼────────┐
                 │   Top_Old    │ │    Top_New   │
                 │ 一致性哈希环 │ │  一致性哈希环 │
                 │  4 nodes     │ │   6 nodes    │
                 └──────────────┘ └──────────────┘
```

- **拓扑层**（`topology.go`）：`ClusterConfig` 描述一次发布的集群配置；
  `Topology` 用一致性哈希环（虚拟节点）把键定位到物理节点。
- **控制器层**（`controller.go`）：
  - `PublishNewTopology` 发布新集群配置，进入双拓扑共存；
  - `SetGrayPercent` / `StepUp` 设定/步进灰度比例；
  - 首次访问某键时按 `hash(key) % 100 < grayPercent` 确定性分桶，
    决策写入键版本目录并**固化**，后续该键所有读写都路由到同一代拓扑；
  - 推移灰度只影响尚未决策的新键，已固化键不漂移 —— 因此可以按 10%
    步长平滑推进到 100%，且任一时刻同一键只写一侧（无双写分裂）；
  - 读未存在的键同样固化其代际，防止“先读后写”场景下的路由漂移。
- **存储层**：`Node` 内置并发安全 KV，用于端到端验证读写无丢失。

## 运行样例

```sh
go run ./cmd/demo
```

复现验收场景：发布新集群配置 → 10% 灰度（新键约 10% 进 Top_New）→
验证读写 → 以 10% 步长推移至 100% → 汇总输出 `lost packets: 0`。

## 测试

```sh
go test -race -v ./...
```

覆盖验收边界：

| 测试 | 验证点 |
|---|---|
| `TestGraySplit10Percent` | 10% 灰度下新拓扑流量占比 ≈10%（10 万键） |
| `TestNoDualWriteSplit` | 灰度 0→100% 全程任一键只写单侧，无分裂 |
| `TestReadYourWritesNoLoss` | 10% 步长推移全程写后读零丢失（含存量键覆写） |
| `TestKeyVersionStickiness` | 灰度变化不影响已固化决策 |
| `TestReadPinsVersion` | 读未命中键也固化代际 |
| `TestSetGrayPercentValidation` | 未发布新拓扑/越界比例被拒绝 |
| `TestStepUpReachesExactly100` | 10% 步长精确到达并钳制在 100% |
| `TestConcurrentNoSplit` | 并发读写 + 并发推移下无分裂无丢失（-race） |
| `TestMetricsAccounting` | 请求统计准确 |
