# starjoin — 星型模式多事实维度 Join 重写（Star-Join Optimization）

识别"中心大事实表 + 周边小维度表"的星型连接，把每个维度表上的 Filter
下推构建为内存位图（Bitmap Join Filter），对事实表只做**一次扫描**，
用组合位图在 Join 之前排除不匹配的外键行（semijoin reduction）。

## 结构

| 文件 | 职责 |
|---|---|
| `table.go` | 列存表（int64 / string 列） |
| `bitmap.go` | 稠密位图（维度主键域上的 bit set） |
| `predicate.go` | 维度谓词：`EqInt` / `BetweenInt` / `EqStr` / `InStr` / `And` / `Or` |
| `optimizer.go` | 星型识别与校验、`Optimize` 构建位图过滤器、`Plan.Execute` 单次扫描过滤 + 幸存者哈希 Join、`ExecuteNaive` 朴素对照计划 |
| `starjoin_test.go` | 单元测试 + 95% 消除率验收测试 + 基准 |
| `cmd/demo/main.go` | 销售事实表 × 时间/门店/商品三维的可运行样例 |

## 执行流程

1. **识别/校验**：事实表行数须大于每个维度表；维度主键唯一；FK/PK 列存在且为整型。
2. **Build 阶段**：扫描每个小维度表，评估下推谓词，把通过的主键点亮到位图中。
3. **Probe 阶段**：单次扫描事实表，每行外键依次探所有位图，任一未命中即丢弃。
4. **Join 阶段**：仅对幸存行做哈希 Join 产出最终元组。

## 运行

```sh
go test -v ./...        # 测试（含 >=95% 消除率验收）
go test -bench . ./...  # 优化计划 vs 朴素计划基准
go run ./cmd/demo       # 运行样例
```

## 验收结果（本机，Apple M1 Pro）

- 销售事实表 2,000,000 行 × 时间维(730) × 门店维(500) × 商品维(5000)
- 组合 Bitmap-Join-Filter 在扫描阶段排除 **99.83%** 的事实行（目标 ≥95%）
- 与朴素哈希 Join 结果完全一致，速度约 **17.7x**（基准：7.5ms vs 140ms）
