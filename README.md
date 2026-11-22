# colarchive

`colarchive` 是一个可导入的 Go 包，用来描述“固定宽度位打包 + 位平面转置”的列归档，
并把逻辑行/位的查询规划成物理字节区间读取计划，同时给出反向解包映射。

## 物理格式

归档由若干 group 顺序拼接而成，第一个 group 从 `Archive.BaseOffset` 开始。
每个 group 包含若干 block。block 之间按 bit 紧密拼接，group 末尾补零到绝对
8 字节边界。

每个 block 独立给出：

- `Rows`：逻辑行数（测试覆盖到 `2^48`，使用 `int64`）；
- `Width`：值位宽；
- `Planes`：长度为 `Width` 的排列。`Planes[p]` 表示物理平面 `p` 存放的逻辑位；
- `Nulls`：空值位图游程。所有游程长度之和必须等于 `Rows`。

设 `nn` 为 block 的非空行数。物理布局为：

```text
group start bit
  block 0: plane 0 (nn bits), plane 1 (nn bits), ..., plane W-1 (nn bits)
  block 1: ...
  ...
  zero padding to next 8-byte absolute boundary
```

对逻辑行 `r`、逻辑位 `b`（0 表示最低有效位）：

```text
q(r)   = r 之前的非空行数
p      = 满足 Planes[p] == b 的物理平面号
offset = blockStartBit + p*nn + q(r)
```

空值行不产生任何数据位。padding 不属于任何 `(row, bit)`，查询和映射永远不会
把它解释为数据。

位序为 MSB-first：物理 bit `t` 位于字节 `t/8` 的第 `7-(t%8)` 位。

## 快速使用

```go
import (
    "fmt"
    ca "prescreen.local/ps-f514817be4"
)

a := &ca.Archive{Groups: []ca.Group{{Blocks: []ca.Block{{
    Rows:   1000,
    Width:  3,
    Planes: []int{0, 1, 2},
    Nulls:  []ca.NullRun{{Rows: 1000, Null: false}},
}}}}}

q := ca.Query{
    Rows: []ca.RowSpan{{Start: 0, Step: 1, Count: 1000}},
    Bits: []int{1},
}

d := ca.Device{
    StartupCost: 10,
    ByteCost:    1,
    MaxBytes:    64,
    Bad:         nil,
}

plan, err := a.Plan(q, d)
if err != nil {
    panic(err)
}
fmt.Println(plan.Cost, plan.TotalBytes, plan.ReadCount)
for _, r := range plan.Reads {
    // Repeat 个事务，事务 k 读 [Start+k*Stride, Start+k*Stride+Length)
    fmt.Println(r.Start, r.Length, r.Repeat, r.Stride)
}
for _, m := range plan.Mapping {
    // 逻辑 (m.RowStart + k*m.RowStep, m.LogicalBit)
    // 对应物理 bit m.PhysicalStart + k*m.PhysicalStep
    _ = m
}
```

## 查询与输出

`Query` 由若干 `RowSpan` 和一个逻辑位集合组成。`RowSpan` 是等差行号：

```go
RowSpan{Start, Step, Count} // Start, Start+Step, ...
```

`Query.Rows` 中任意一行都必须落在归档范围内，否则返回 `ErrRowOutOfRange`。
`Query.Bits` 会被排序去重；如果某个被查询 block 的 `Width` 不包含该位，返回错误。

`Plan` 包含：

- `Reads []Read`：压缩的读计划。`Read` 表示 `Repeat` 笔事务，第 `k` 笔为
  `[Start+k*Stride, Start+k*Stride+Length)`。规则读区间不会逐个展开。
- `Mapping []MapPiece`：反向解包映射。每个 piece 是一段等差逻辑行映射到
  一段等差物理 bit；空值行不进入 `Mapping`。
- `Nulls []NullSpan`：被查询到的空值行（同样用等差段表示）。
- `Cost`、`TotalBytes`、`ReadCount`：按压缩表示直接计算的总成本、读取字节数
  和事务数。
- `RequiredRuns`：当计划走显式精确路径时给出最小必需字节区间；超大压缩路径
  可能为 `nil`。

`Plan.Expand(limit)` 可以把读计划展开成单个 `ByteInterval`，超过 `limit`
时拒绝执行，避免误把巨大规则输出全部展开。`Plan.LogicalAt(physicalBit)` 用
映射 piece 做物理 bit 到逻辑 bit 的反查。

## 成本模型与最优性

一笔读取的成本为：

```text
StartupCost + ByteCost * Length
```

读取长度不得超过 `MaxBytes`；`Bad` 中的绝对字节区间不可触碰、不可跨越。
设备允许“多读”：两段必需字节之间的小间隙可以被包含进同一笔读取，但按字节
计费。

规划器先求所有必需字节区间，并把相邻/重叠的必需字节合并成最大连续 run。
在显式路径中，对 run 的分组做动态规划：把连续 run `i..j` 合成一组意味着读取
`[run[i].Start, run[j].End)`；该组需要 `ceil(span/MaxBytes)` 笔事务。是否跨过
某个间隙由 `StartupCost` 与 `ByteCost*gap` 的权衡决定，长 run 会自动拆成多笔
事务。

比较顺序为：

1. 总成本最小；
2. 总读取字节数最少；
3. 展开后的读区间序列按 `(Start, End)` 字典序最小。

如果 `StartupCost == 0`，事务数量不受惩罚；为满足第三级排序，连续必需字节会
表示成 1 字节的 `Repeat`/`Stride` 段，而不是任意合并成大块。

显式路径的结果与暴力枚举所有分组一致（见测试）。

## 巨大等差查询

对 `Rows` 达到 `2^48` 的均匀 block，查询行号、反向映射和读计划都可以保持
压缩：

- 逻辑行用 `RowSpan`/`MapPiece` 等差段表示；
- 同一物理平面上的连续行区间只占一个 `MapPiece`；
- 对步长大于 1 的位序列，必需字节位置按 `lcm(step,8)` 形成周期；规划器把它
  分解为少量恒相位段，每段生成带 `Repeat`/`Stride` 的读事务；
- 连续的长区间压缩为“一个短首笔 + 若干 `MaxBytes` 重复笔”；
- 单个均匀等差 run（等长区间、等步长）走独立的算术求解器，直接比较可用的
  分组大小，不展开每个周期。

因此不会按行数或按规则读区间数量分配内存。若查询被大量不规则空值游程切得
过碎，显式路径有 `Planner.ExactLimit`（默认 4096 个区间）上限；超过后走周期
压缩路径。极端碎片化时可能返回 `ErrTooComplex`。

## 测试

`go test ./...` 包含：

- 按 bit 独立构造小文件，验证平面换序、空值游程边缘、跨 block 的 bit 级拼接；
- 在 group 尾部 padding 旁设置坏区，确认读计划不会把 padding 当数据，也不会
  跨越坏区；
- 与暴力分组枚举对照的随机成本最优性测试；
- `2^48` 行 block 上的连续查询和大步长等差查询，确认输出仍是重复段；
- 长时间隔重复读只碰到一个坏字节时，按副本裁剪而不是展开全部事务。

## 限制

- 成本、字节数和事务数使用 `int64`；溢出时返回错误。
- `StartupCost`、`ByteCost` 必须非负，`MaxBytes` 必须为正。
- 一个查询位集合会应用到所有命中 block；不同位宽的 block 需要分开查询。
- padding 只用于对齐，没有对应的逻辑映射。
