# segint

线段相交工具（Go）：计算整数坐标线段集合的**全部交点**与**共线重叠段**，
坐标以规范有理数（`math/big.Rat`，恒为约分形式）精确表示。

## 语义

- 端点接触算相交；零长度线段视为点。
- 多条线段同点相交时，该点只报告一次，并保留全部参与线段 ID 集合（排序去重）。
- 共线且重叠长度为正的一对线段产生重叠段；同一支撑线上的重叠被合并为
  极大的原子区间，每段附带**恰好覆盖它的**线段 ID 集合。
- 共线但仅共享单个点的线段对产生交点而非重叠段。

## 用法

```go
segs := []segint.Segment{
    segint.NewSegment(1, 0, 0, 3, 1),
    segint.NewSegment(2, 1, 0, 0, 1),
}
res := segint.Intersect(segs) // 扫描线实现
// res.Points[0].Point == (3/4, 1/4)，SegmentIDs == [1 2]
```

- `Intersect` / `IntersectSweep`：x 方向扫描线。同 x 事件按“先插入后删除”
  排序，因此竖线、零长度段（零宽 x 区间）与端点相触（x 区间仅相切）都能
  正确配对；进入活动集的线段与当前活动集逐一做精确求交。
- `IntersectPairwise`：O(n²) 全配对参考实现，用于核对（见
  `TestSweepMatchesPairwiseRandom`，含竖线、零长度段与多重共线覆盖的随机对拍）。

## 测试

```sh
go test ./...
```
