# cbitmap — 压缩位图 + 细粒度度量监控（Go，仅标准库）

Roaring 风格的压缩位图，支持 Array / Bitmap / RLE(Run) 三种容器的自动选择与
转换，提供 AND / OR / XOR 及批量（Many）布尔集合运算；内置高精度时序度量采集
（延迟分布、命中率、错误码、容器转换计数）与 JSON 聚合导出。全部 API 并发安全。

## 结构

- `container.go` — 三种容器实现及自动转换阈值（array ≤4096；run ≤2048 段）
- `ops.go` — 容器级 AND/OR/XOR（覆盖全部 9 种类型组合）
- `bitmap.go` — `Bitmap` 主类型：`Add/AddRange/Remove/Contains/Cardinality/
  Optimize/Clone/Equals/ToArray` 与 `And/Or/Xor/AndMany/OrMany/XorMany`
- `metrics.go` — 度量：原子计数器、指数桶延迟直方图（P50/P90/P99）、定间隔
  时序桶（惰性轮转，无后台 goroutine）、错误码分类计数、`Snapshot`/
  `ExportJSON` 聚合导出、可启停的 `Flusher`
- `cmd/example/main.go` — 运行样例

## 运行

```sh
go test -race ./...     # 测试（含竞态检测）
go run ./cmd/example    # 样例：稀疏 ID + 密集区间 + 百亿级模拟 + JSON 导出
```

## 样例输出（节选）

```
containers of a: map[array:3 bitmap:0 run:2]
cardinality a: 100005  b: 100003
AND: 50003 values in 1.222792ms
OR:  150005 values in 28.875µs
XOR: 100002 values in 37.625µs
simulated 1e10 values: intersected 5000000000 values in 140.636875ms (35159 us/op)
--- metrics snapshot (JSON) ---
{ "inserts": 200009, "hit_rate": 0.5, "and_ops": 2, ...
  "latency": { "count": 7, "p50_ns": 3200, "p90_ns": 12800, ... },
  "errors": { "invalid_range": 1 } }
```

## 验收要点

- 稀疏离散 ID → ArrayContainer；密集连续区间 → RunContainer；不可压缩的高
  基数集合 → BitmapContainer，阈值边界（4096/4097、run 段数）自动双向转换。
- 百亿（1e10）级数据量模拟：4 分片 × 2.5e9 值的稠密位图，单次交/并运算在
  毫秒级完成（RLE 容器下每次容器相交为 O(1)）。
- 测试覆盖：容器运算全类型组合矩阵、map 参考模型随机模糊对照、批量运算与
  顺序运算等价性、度量计数/命中率/错误码/直方图分位数/时序轮转与容量、
  JSON 导出可解析性、8 写 2 读并发安全（`-race`）、Flusher goroutine 无泄漏。
