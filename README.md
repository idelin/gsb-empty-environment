# occlusion — 角域遮挡查询库

相机视野为圆周 `[0, 360)`。遮挡物由有理数起始角与顺时针跨度 `[0, 360]`描述，
不同高度层对应不同优先级。库对每个可见角输出最高优先级物体（同优先级取最小编号），
并给出覆盖整圆的最少圆弧分段。

## 语义约定

- 角度全部使用 `*big.Rat` 精确有理数运算，端点比较无浮点误差。
- 区间左闭右开：`[start, start+span)`；共享端点属于顺时针方向的下一段。
- 跨度 `0` 为空（不覆盖任何角，包括自己的起点）；跨度 `360` 为整圆，
  即使起止点重合也绝不视为空。
- 区间可跨越 0° 子午线（如 `[350, +30)` 覆盖 `[350,360) ∪ [0,20)`）。
- 未被任何遮挡物覆盖的角度，赢家为 `occlusion.NoObject`（-1）。

## API

```go
m := occlusion.New()
m.Upsert(occlusion.Occluder{ID: 7, Priority: 2, Start: occlusion.Rat(270, 1), Span: occlusion.Rat(180, 1)})
m.Upsert(occlusion.Occluder{ID: 3, Priority: 1, Start: occlusion.Rat(0, 1),  Span: occlusion.Rat(360, 1)})
m.Remove(7)              // 删除
m.Upsert(...)            // 同 ID 再次 Upsert 即修改

w := m.WinnerAt(occlusion.Rat(45, 1))  // 单角查询 -> 3
segs := m.Segments()                   // 最少分段，覆盖整圆
err := m.CheckConsistency()            // 与朴素采样模型核对
```

`Segments()` 返回 `[]Segment{Start, Span, Winner}`：每段为顺时针弧
`[Start, Start+Span)`，`Start ∈ [0,360)`，`Span ∈ (0,360]`；各段首尾相接恰好覆盖整圆，
且环上相邻段（含跨 0 处）赢家必不同——分段数最少。`Span == 360` 表示整圆一段。

## 正确性核对

`CheckConsistency()` 实现“角度端点间取样”的朴素模型核对：收集所有遮挡物端点，
在每对相邻端点之间取有理数中点暴力求赢家，与分段结果逐点比对；同时校验整圆恰好覆盖、
左闭端点归属、分段最少性。`TestRandomConsistency` 以 300 组随机遮挡物（含零跨度、
整圆、跨零、负起始角、同层同 ID 并列）做模糊核对。

## 测试

```sh
go test ./...
```
