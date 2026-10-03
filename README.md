# starjoin — 星型模式多维度 Join 重写优化（Go 标准库实现）

数据仓库星型模型优化器：识别中心大事实表与周边小维度表的连接关系，
把每个维度表上的 Filter 重写为内存中的小型 **Bitmap-Join-Filter**，
在对事实表做全表扫描时执行**一次性多维过滤**，先排除不匹配外键行，
再对幸存行做精确 Hash Join。

## 工作原理

1. **星型识别**（`BuildPlan`）：不出现在任何 Join 边维度侧、且行数最大的表
   被识别为事实表；每条 Join 边携带维度过滤谓词。
2. **过滤器重写**（`buildFilter`）：扫描维度表一次，应用谓词，把存活的维度
   主键物化为位图。整型键使用 `max(key)+1` 的精确直寻址位图；非整型键
   退化为 FNV 哈希位图（Bloom 风格，假阳性由后续精确 Join 消除）。
3. **一次性多维过滤**（`Plan.Execute`）：对事实表单趟扫描，逐行对所有
   Bitmap-Join-Filter 做 O(1) 探测，任一不命中即丢弃；幸存行再做精确
   Hash Join 产出最终结果。

## 目录结构

- `bitmap.go` — 基于 `[]uint64` 的位图
- `table.go` — `Row` / `Table` 抽象
- `optimizer.go` — 星型识别、Bitmap-Join-Filter 构建与执行、朴素基线 `NaiveJoin`
- `starjoin_test.go` — go test 验收测试与基准
- `cmd/demo/main.go` — 运行样例（销售事实表 × 时间/门店/商品维）

## 运行

```sh
go test -v ./...     # 测试
go run ./cmd/demo    # 样例
```

## 验收结果（本机实测）

销售事实表 500,000 行，维度谓词 `quarter='Q1'`（90/360）、
`region='East'`（25/100）、`category='Electronics'`（100/2000）：

```
optimizer: detected fact table "sales", built 3 Bitmap-Join-Filters
  time_dim     keys=90   bitmap=361 bits (45.1 bytes)
  store_dim    keys=25   bitmap=101 bits (12.6 bytes)
  product_dim  keys=100  bitmap=2001 bits (250.1 bytes)

naive hash join : scanned=500000 probes=656003 joined=1576 took=23.9ms
bitmap optimized: scanned=500000 bitmap-passed=1576 excluded=498424 (99.68%) joined=1576 took=15.0ms

results match (1576 rows). join-stage speedup: 1.6x
```

位图阶段在全表扫描中排除 **99.68%** 的不匹配外键行（验收要求 ≥95%），
优化结果与朴素 Hash Join 完全一致。

测试摘要：

```
--- PASS: TestBitmap
--- PASS: TestStarSchemaDetection
--- PASS: TestBitmapJoinFilterContents
--- PASS: TestOptimizedMatchesNaive
--- PASS: TestExclusionRateAbove95Percent   (excluded 199349/200000 = 99.67%)
--- PASS: TestHashedBitmapForStringKeys
--- PASS: TestBuildPlanErrors
```
