# clockcal — 多通道时钟校准器

每个通道的本地时钟按 `global = a·t + b` 映射到全局时间，其中漂移率
`a` 为有理数、偏移 `b` 为整数。校准点带有误差区间：在本地时刻 `t`，
真实全局时间落在 `[lo, hi]`，即约束 `lo ≤ a·t + b ≤ hi`。

本工具对每个通道求出**精确**（`math/big.Rat` 有理运算）的可行参数范围，
隔离不相容校准点，并在对齐事件后保留**偏序**——区间仍重叠的事件对
保持不可比较，绝不按估计中点强行排序。

## 构建与运行

```sh
go build ./cmd/clockcal
./clockcal examples/demo.json     # 或从 stdin 读入：./clockcal < input.json
go test ./...
```

## 输入格式（JSON）

```json
{
  "channels": [
    {
      "name": "sensor-A",
      "drift_bounds": {"min": "9/10", "max": "11/10"},   // 可选的先验漂移约束
      "calibration": [
        {"id": "A0", "local": "0",   "global": {"min": "10",  "max": "12"}},
        {"id": "A1", "local": "100", "global": {"min": "110", "max": "114"}}
      ],
      "events": [{"id": "boot", "local": "2"}]
    }
  ]
}
```

- 数值可写 JSON 数字或字符串；字符串支持整数、小数与分数 `"p/q"`，
  全部按精确有理数处理。
- `global.min` / `global.max` 必须有限（校准区间有界）；`drift_bounds`
  的任一侧可为 `null` 或省略。

## 输出

- `channels[].drift_rate`：可行漂移率区间 `[a_min, a_max]`（精确分数）。
- `channels[].offset` 与 `offset_integer_min/max`：偏移的实数区间与其中
  可行的整数偏移范围（凸集投影保证区间内每个整数都可取到）。
- `channels[].incompatible_calibration`：不相容校准点集合。若全部校准点
  不可同时满足，求出**最大相容子集**（删除点数最少；超出搜索预算时退化
  为确定性贪心），被删除的点即不相容点，参数范围由剩余点给出。
- `events[].global_interval`：事件全局时间的精确可能区间（已考虑整数
  偏移约束，非连续松弛）。`null` 表示该侧无界。
- `partial_order.edges`：确定先后关系（已做传递归约）。`e ≺ f` 当且仅当
  `f` 的最早可能时间严格大于 `e` 的最晚可能时间。
- `partial_order.incomparable`：区间仍可能重叠、先后无法确定的事件对——
  保留为偏序，不按中点排序。

## 方法要点

- **可行域**：约束 `lo_i ≤ a·t_i + b ≤ hi_i` 是 `(a, b)` 平面上的半平面。
  漂移率区间由所有点对约束 `a·(t_j − t_i) ≤ hi_j − lo_i` 直接相交得到；
  偏移上下界是在该区间上对分段线性函数 `U(a)=min(hi_j−a·t_j)`、
  `L(a)=max(lo_i−a·t_i)` 求极值（候选点为直线两两交点，精确）。
- **整数偏移**：偏移可行集是实数区间 `[b_min, b_max]` 中的整数
  （凸集投影），存在性即 `ceil(b_min) ≤ floor(b_max)`。
- **事件区间**：固定整数 `b` 时最优 `a` 在可行漂移区间端点取得，故最优
  值是 `b` 的分段线性函数；其折点为约束边界线两两交点（解出 `b`），
  整数最优必在某折点或区间端点相邻的整数处。无界侧由射线方向的斜率
  精确判定。枚举这些候选整数即得精确事件区间。
- **偏序**：只使用严格区间分离 `hi(e) < lo(f)` 建立先后；相触
  （`hi == lo`）或重叠的事件对保持不可比较。

## 目录

- `cmd/clockcal/` — CLI 与 JSON I/O
- `internal/calib/` — 精确可行域、整数偏移、事件区间、最大相容子集
- `internal/order/` — 偏序构建与传递归约
- `examples/demo.json` — 示例输入
