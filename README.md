# 多版本路由拓扑渐进式迁移控制器

纯 Go 标准库实现。将流量从旧拓扑 `Top_Old` 平滑迁移到新拓扑 `Top_New`，
支持百分比灰度推移，并在双拓扑共存期间通过**键版本注册表**保证每个键
在任意时刻只有唯一的读写通道，杜绝双写分裂。

## 设计

- **拓扑（topology.go）**：一致性哈希环（虚拟节点），将键映射到物理节点。
- **路由决策（controller.go）**：
  - `bucket = crc32(key) mod 100`，`bucket < percent` 的键**应当**归属 `Top_New`，
    其余归属 `Top_Old` —— 确定性切分，非随机。
  - **键版本注册表**记录每个键**当前**归属的拓扑代际（GenOld/GenNew）。
    读请求按注册版本路由，绝不同时读两个拓扑。
  - 当期望代际与注册版本不一致时，在**键级分段锁**内执行迁移
    （从旧拓扑读出 → 写入新拓扑 → 原子翻转版本），随后服务本次请求。
    版本翻转是写通道的唯一交接点，因此任何时刻每个键只有一个写入者。
- **灰度推移**：`SetTrafficPercent(p)` 以任意步长（验收用 10%）调整；
  `VerifyNewCluster()` 在推移前对新集群每个节点做写/读/删探针。

## 运行

```sh
go test -v -race ./...   # 测试
go run ./cmd/demo        # 样例：10% 步长从 Top_Old 推移到 Top_New，全程零丢失
```

## 样例输出

```
seeded 1000 keys on Top_Old
published new cluster config (Top_New)
traffic= 10%  keys on Top_New=  97/1000  lost reads=0
traffic= 20%  keys on Top_New= 200/1000  lost reads=0
...
traffic=100%  keys on Top_New=1000/1000  lost reads=0
migration complete: total lost reads across rollout = 0
OK: zero loss during progressive migration
```

## 测试覆盖

- `TestCanarySplitAt10Percent`：10% 灰度下键归属与确定性分桶完全一致。
- `TestProgressiveMigrationNoLoss`：10%→100% 逐步推移，每步校验全部键无丢失。
- `TestConcurrentReadWriteNoSplitBrain`：推移过程中并发读写，值单调不回退、
  最终读等于最终写（`-race` 通过）。
- `TestVerifyNewCluster` / `TestSetTrafficPercentValidation` / `TestDelete`。
