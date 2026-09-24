# 精确 Bezier 曲线折线细分

本项目使用 Python 3.11 标准库实现二次或三次 Bezier 曲线的自适应折线化。所有坐标、参数区间和误差平方都使用 `fractions.Fraction` 精确表示，不使用浮点 epsilon 做共线、投影落区、端点连续或停止细分判断。

## API

```python
from fractions import Fraction
from bezier_flatten import flatten_bezier, flatten_bezier_intervals

points = [(0, 0), (0, 8), (8, 8)]
tolerance = Fraction(1, 10)

segments = flatten_bezier(points, tolerance, max_depth=64)
intervals = flatten_bezier_intervals(points, tolerance, max_depth=64)
```

3 个控制点表示二次曲线，4 个控制点表示三次曲线。每个 `FlattenSegment` 包含：

- `t_start`、`t_end`：原曲线参数空间中的相邻区间。
- `start`、`end`：对应原曲线上的精确端点，相邻折线端点完全相等。
- `squared_bound`：该段几何误差上界的平方；接受条件是 `squared_bound <= tolerance ** 2`。

`max_depth` 是允许的二分深度：根区间深度为 0，深度上限为 `d` 时最多产生 `2**d` 个区间。达到该深度仍不能通过几何判定时抛出 `FlattenError`，消息明确写明未达标区间、深度、几何平方界和允许平方误差。

## 几何误差保证

对每个子曲线，取其两个端点形成封闭弦线段，并计算所有内部控制点到该封闭线段的最大平方距离。

1. Bernstein 基函数非负且和为 1，因此子曲线位于控制点的凸包内。
2. 对固定封闭线段的平方欧氏距离是凸函数。
3. 曲线点是控制点的 Bernstein 凸组合，所以曲线点到弦线段的平方距离不超过控制点到弦线段平方距离的最大值。

因此接受条件直接保证该参数区间内每个曲线点到对应折线段的距离不超过 `tolerance`。判定中使用点积与封闭线段的三个精确区域：投影参数小于 0、大于 1、位于闭区间内；共线但越过端点时距离为到最近端点的非零距离，必须继续细分。

## 退化情形

- 控制点都在端点弦线段内部或端点上：`squared_bound = 0`，直线段一段完成。
- 所有控制点相同：曲线退化为一个点，输出单个零长折线区间 `[0, 1]`。
- 端点相同但内部控制点不为同一点：弦退化为点，距离按到该点计算；若超过误差则继续细分。
- 共线但控制点越过端点：不能仅凭“共线”接受，需细分到单调且落入几何界内。
- 尖点：尖点附近不会因为速度为零而提前停止；只有几何距离界通过时才接受该区间。

非法输入同样抛出 `FlattenError`：控制点数量必须是 3 或 4；每点必须是二维；坐标和容差必须是 `int` 或 `Fraction`；容差必须为正；`max_depth` 必须是非负整数。

## 运行样例

```bash
python3.11 example.py
```

本机输出摘要：

```text
straight quadratic control points
  pieces: 1
  intervals: [(Fraction(0, 1), Fraction(1, 1))]

quadratic arc with rational tolerance
  pieces: 8
  intervals: [(0, 1/8), (1/8, 1/4), ..., (7/8, 1)]

cubic cusp at t=1/2
  pieces: 10
  intervals: [(0, 1/16), (1/16, 1/8), ..., (7/8, 1)]

depth limit exhausted
  FlattenError: error bound not met before depth limit: ...
```

## 测试

测试只使用标准库 `unittest`：

```bash
python3.11 -m unittest discover -s tests -v
```

已运行结果：

```text
test_all_control_points_equal_is_degenerate_point ... ok
test_collinear_overshoot_requires_subdivision ... ok
test_cubic_cusp_near_midpoint_keeps_subdividing ... ok
test_depth_limit_reports_not_met ... ok
test_equal_endpoints_with_different_interior_still_subdivides ... ok
test_invalid_arguments_are_rejected ... ok
test_quadratic_actual_geometric_error_is_within_tolerance ... ok
test_quadratic_rational_coordinates_are_exact_and_partitioned ... ok
test_straight_cubic_is_one_segment ... ok
test_straight_quadratic_is_one_segment ... ok

----------------------------------------------------------------------
Ran 10 tests in 0.342s

OK
```
