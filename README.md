# timeline — 时间线压缩与多轨同步映射工具

压缩长空闲段，保持事件次序与同刻关系；所有压缩段共享一条单调分段线性映射。
鼠标拖动区间与音频连续段为受保护段，不可压缩。全程使用精确有理数（`math/big.Rat`），
帧取样统一半偶舍入。

## 运行

```sh
go run . -spec examples/spec.json -o report.json
go test ./...
```

## 输入（spec.json）

- `config`: `min_idle`（可压缩空闲阈值）、`ratio`（压缩比，(0,1]）、`min_keep`（压缩后时长下限，>0）
- `events`: 事件时刻 `t`（有理数字符串，如 `"1/30"`）、`track`、`kind`、`label`
- `protected`: 受保护区间 `[start, end)`，`kind` 如 `mouse-drag`、`audio-continuous`
- `tracks`: 每轨有理起点 `start` 与采样间隔 `interval`
- `sync_points`: 跨轨同步点时刻列表

## 方法

1. **找空闲段**：相邻事件时刻之间，扣除全部受保护区间后的剩余区间片；
   长度超过 `min_idle` 才可压缩，目标时长 `max(min_keep, 长度*ratio)`。
2. **共享映射**：压缩段内斜率 = 目标/原长 ∈ (0,1)，其余斜率 = 1；
   由关键点（knot）表示的分段线性函数，严格递增故可逆。
3. **帧取样**：映射后帧序号 = `round_half_even((t' - start') / interval)`。
4. **反向定位**：压缩后时间经逆映射返回原时间区间；帧定位返回
   帧窗口 `[center ± interval/2]` 反映射后的原时间区间。

## 输出（report.json）

- `mapping`: 正映射关键点 `knots` 与反映射关键点 `inverse_knots`
- `segments` / `skipped`: 每段压缩/跳过的理由
- `tracks`: 每轨 `mapped_start` 与逐检查点偏移证据（原帧位置、映射帧位置、半偶舍入帧号、帧偏移）
- `sync_points`: 每个同步点的反向定位区间与各轨帧窗口（均须覆盖原同步时刻）
- `verification`:
  - `key_points` — 关键点 T、M 严格递增（斜率>0，可逆范围覆盖全时间轴）
  - `overlap_checks` — 压缩段两两无重叠；压缩段与受保护段在原域和映射域均无内部重叠
  - `event_order` — 事件次序保持，同刻必同像、异刻必异像
  - `round_trip` — 关键点/事件/同步点/轨道起点满足 逆(正(t))=t 且 正(逆(m))=m
  - `sync_summary` — 跨轨同步点核对汇总

核对任一项失败时进程以退出码 1 结束。
