# hashring — 拓扑感知的主备双环哈希路由控制器

纯 Go 标准库实现。维护 **主环（Active）** 与 **备环（Standby）** 两个一致性哈希环：

- **主环承载全量流量**：`Lookup` / `LookupN` 全部走无锁原子读。
- **备环后台同步拓扑变更**：`SyncAdd` / `SyncRemove` 只作用于备环 staging 区，主环零打扰。
- **预热（Prewarm）**：备环冻结为不可变环，并预先计算键迁移影响（精确到哈希空间
  分段的迁移比例、每节点得失、采样键交叉验证），产出 `MigrationPlan`。
- **一键指针翻转（Flip）**：单次 `atomic.Pointer` 存储完成全局切换，纳秒级完成；
  环对象不可变，读者只会看到完整的旧环或完整的新环，绝不出现中间态。

## 拓扑感知副本放置

节点携带 `Zone` / `Rack` 故障域标签。`LookupN(key, n)` 沿环选取 n 个不同节点时，
优先散布到不同 Zone，其次不同 Rack，最后才允许复用故障域。

## 结构

| 文件 | 说明 |
|---|---|
| `ring.go` | 不可变一致性哈希环（虚节点 + fmix64 混合）与 `Builder` |
| `controller.go` | 双环控制器：staging / Prewarm / Flip / MigrationPlan |
| `ring_test.go` | 环行为测试（确定性、拓扑散布、最小扰动） |
| `controller_test.go` | 控制器验收测试（并发流量下缩容 + 原子翻转） |
| `cmd/demo/main.go` | 端到端运行样例 |

## 运行样例

```sh
go run ./cmd/demo
```

实测输出（12 节点 / 3 可用区，运行期间向备环注入 -3 节点缩容）：

```
[boot] active ring v1: 12 nodes, serving 100% traffic
[traffic] 615898 lookups served on active ring, errors=0
[standby] staged scale-in: -3 nodes (54.542µs)
[prewarm] standby ring v2 built in 431.76875ms
[prewarm] removed=[node-03 node-07 node-11]
[prewarm] exact hash-space moved: 24.2016% (sampled: 24.2300% over 200000 keys)
[flip] pointer swapped in 333ns; now serving ring v2 with 9 nodes
[done] total lookups=2288230, routing errors=0
[verify] key user:0:1 -> node-08, replicas=[node-08(zone-c) node-00(zone-a) node-01(zone-b)]
```

## 测试

```sh
go test -race -v ./...
```

结果（Go 1.24，darwin/arm64，全部通过）：

```
--- PASS: TestNewControllerRequiresNodes (0.00s)
--- PASS: TestFlipWithoutPrewarmFails (0.00s)
--- PASS: TestSyncRemoveUnknownNodeFails (0.00s)
--- PASS: TestPrewarmPlanAccuracy (0.02s)
--- PASS: TestAcceptanceScaleInWithConcurrentTraffic (0.46s)
        flip completed in 459ns
        66571 concurrent lookups, 0 routing errors
        moved: plan(exact)=0.1610 plan(sampled)=0.1607 actual=0.1608
--- PASS: TestConcurrentFlipRace (0.81s)
--- PASS: TestLookupDeterministicAndMember (0.02s)
--- PASS: TestLookupEmptyRing (0.00s)
--- PASS: TestLookupNDistinctAndTopologyAware (0.27s)
--- PASS: TestLookupNClampsToNodeCount (0.00s)
--- PASS: TestBuilderRemove (0.00s)
--- PASS: TestMinimalDisruptionOnRemoval (0.98s)
```

## 验收边界对照

`TestAcceptanceScaleInWithConcurrentTraffic` 完整覆盖验收场景：

1. 主环 12 节点平稳运行，8 个并发读协程持续路由（Lookup + LookupN）。
2. 流量不中断的情况下向备环注入 2 节点缩容（`SyncRemove`）。
3. `Prewarm` 构建备环并预计算迁移影响（精确 16.10%，采样 16.07%）。
4. `Flip` 原子翻转，实测 **459ns**（远低于毫秒级要求）。
5. 全程 **0 路由错乱**：无空路由、无返回非环成员节点、翻转后无已下线节点承流。
6. 翻转后实际键迁移比例 16.08%，与预计算计划吻合；控制器可继续下一轮变更。
