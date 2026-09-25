# 拓扑保持的共享边界折线简化

对一组可能共享边界的折线/多边形做简化：在误差阈值内减少点数，同时保证
**不产生新交叉、不翻转/压塌环、不封死狭窄通道、共享边界简化后完全一致**，
并为每个被删点提供精确的误差上界。全部计算使用精确有理数
（`fractions.Fraction`），**没有任何 epsilon 修补**。仅依赖标准库。

## 文件

- `geom.py` — 精确几何谓词：定向符号、线段关系（相离/正交/触碰/共线重叠）、
  点到线段距离平方、有向面积，全部用 `Fraction` 精确求值。
- `simplify.py` — 网络简化主算法，`simplify_network(polylines, tol)`。
- `demo.py` — 运行样例（`python3.11 demo.py`）。
- `test_simplify.py` — 21 个 unittest 用例（`python3.11 -m unittest test_simplify -v`）。

## 算法

1. **建平面图**：把所有输入线段在交点、T 型接触点、共线重叠端点处精确
   剖分成原子边；重合的共享边合并为同一条边。
2. **提取链（chain）**：度 ≠ 2 的顶点、开放折线端点是"结点"（junction），
   结点之间的极大路径是一条链。共享边界只属于**一条**链，只简化一次，
   因此两个区域简化后的共享边界逐点完全一致。
3. **拓扑保持的 Douglas–Peucker**：每条链递归地用弦替代子链。弦被接受
   当且仅当：
   - 子链所有点到弦的距离 ≤ tol（精确比较平方距离与 tol²）；
   - 弦与全网当前任何边不正交、不触碰（弦自身端点除外）、不共线重叠
     —— 因此不会产生新交叉，狭窄通道不会被封死；
   - 对环额外要求：结果至少 3 个顶点且有向面积符号不变（精确符号），
     因此环不会翻转或压塌。
4. **重建与误差报告**：按原折线的边序列从简化后的链重组输出；每个被删
   原始顶点到简化折线的精确平方距离作为误差上界返回（构造保证
   ≤ tol²）。

## 退化情形定义

- 空折线 / 单点：原样返回。
- 连续重复点：作为严格冗余点删除（误差 0）。
- 不同点少于 2 个的环：坍缩为单点。
- 零面积环：可删除共线冗余点，永不翻转（面积 0 时方向无定义）。
- `tol = 0`：只删除严格冗余点（恰好落在相邻保留点连线上的点，且需通过
  拓扑检查）。
- 自交输入：交点成为结点；输出保证不引入**新**交叉。
- 浮点坐标直接拒绝（`TypeError`），只接受 `int` / `Fraction`，保证精确性。

## API

```python
from simplify import simplify_network

res = simplify_network(polylines, tol=1)   # tol: int 或 Fraction
res.polylines      # 简化后的折线（与输入同序同结构，环仍闭合）
res.removed        # list[RemovedPoint]，含 polyline/index/point/error_sq
res.max_error_sq   # 全部被删点的最大平方误差（精确 Fraction）
```

## 运行样例（`python3.11 demo.py` 节选）

```
=== Two regions sharing a boundary (tol=1) ===
  polyline 0: 8 -> 5 points
  polyline 1: 8 -> 5 points
  removed 6 vertices, max error^2 = 1 (max error ~= 1.0000)
  shared boundary identical in both regions:
  [(Fraction(2, 1), Fraction(0, 1)), (Fraction(2, 1), Fraction(4, 1))]

=== Narrow channel (arch over a wall) (tol=3) ===
  polyline 0: 4 -> 3 points
    [(0,0), (4,2), (12,0)]          # 朴素 DP 的弦 (0,0)-(12,0) 会穿墙，被拒绝
  polyline 1: 2 -> 2 points         # 通道保持开放

=== Rational coordinates, tol=1/2 ===
  polyline 0: 5 -> 3 points
  removed 2 vertices, max error^2 = 16/85 (max error ~= 0.4339)

=== tol=0 (only strictly redundant points) ===
  polyline 0: 6 -> 4 points         # 仅删除两个共线冗余点，error^2 = 0

=== Flat triangle ring, tol=2 ===
  polyline 0: 4 -> 4 points         # 环不压塌、不翻转
```

## 测试结果

```
$ python3.11 -m unittest test_simplify -v
test_all_identical_ring_collapses_to_point ... ok
test_empty_polyline ... ok
test_single_point ... ok
test_two_points_unchanged ... ok
test_zero_area_ring_never_flips ... ok
test_error_bound_holds_on_network ... ok
test_removed_points_within_tolerance ... ok
test_duplicate_points_removed_at_tol_zero ... ok
test_float_coordinates_rejected ... ok
test_rational_coordinates_exact ... ok
test_tol_zero_keeps_noncollinear_points ... ok
test_tol_zero_removes_only_strictly_redundant ... ok
test_grid_of_four_regions ... ok
test_partial_shared_boundary_t_junctions_kept ... ok
test_shared_boundary_identical_after_simplification ... ok
test_shared_boundary_identical_at_tol_zero ... ok
test_shared_collinear_midpoint_removed_from_both ... ok
test_narrow_channel_not_sealed ... ok
test_reversed_ring_orientation_preserved ... ok
test_ring_orientation_and_min_vertices ... ok
test_square_ring_stays_simple_under_huge_tol ... ok
----------------------------------------------------------------------
Ran 21 tests in 0.073s

OK
```
