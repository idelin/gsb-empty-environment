# wavelet — 整数 Wavelet Tree（纯标准库 Go 实现）

对整数序列构建 wavelet tree，支持：

- `Access(i)`：原序列第 i 个值（坐标解压还原，负数正确）
- `KthSmallest(l, r, k)`：区间 `[l,r)` 第 k 小（0 起），重复值按重数计，空区间/越界返回 `ErrEmptyRange`
- `RangeFreq(l, r, lo, hi)`：区间 `[l,r)` 内值落在 `[lo,hi]` 的个数
- `Freq(l, r, v)`：区间内某值的频数

## 编码布局（非 map 包装）

- 构建时对值做坐标压缩：升序去重表 `vals []int`（秩 ↔ 原值双向映射），
  原序列转为**保持原顺序**的秩序列 `ranks []int`（原序映射），查询不需要对子数组排序。
- 树共 `ceil(log2 σ)` 层（σ 为去重后值域大小），每层一个 `bitVector`：
  - 比特打包进 `[]uint64`（64 bit/字，小端位序）；
  - 每 512 bit 一个 `uint64` 超块记录前缀 popcount，rank 为 O(1)。
- `Bytes()` 返回真实占用：各层位向量（比特数组 + rank 超块）+ 映射表 + 原序秩序列。
  例：n=1000、σ=1001 时约 28 KB（≈ n·log σ bit + 索引开销）。

## 运行

```sh
go test ./... -v   # 含随机对拍测试与 Example 样例
go run ./cmd/demo  # 运行样例
```
